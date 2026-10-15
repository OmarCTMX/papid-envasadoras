// Dashboard de Envasadoras (pantalla de valvuladoras de un silo).
//
// Fuentes de datos:
//   - POST /api/orden (la manda el admin): orden + personal. Llena el header
//     (orden, título, lotes) y el footer (personal). Reinicia los contadores.
//   - NATS papid.envasadora.<MACHINE_CODE> (PLC vía Node-RED/distribuidor):
//     peso de las bolas, contador de bultos, setpoint, columna y LEDs.
//
// El dashboard calcula los bultos de la orden (suma de las envasadoras), los
// lotes completados (⌊bultos / cantidadBts⌋) y la tabla de bultos, y lo guarda
// todo en NATS KV para sobrevivir reinicios. Se reinicia a diario a RESET_HORA.
//
// El navegador recibe el estado por SSE (evento "estado").
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"

	"papid-envasadoras/internal/api"
	"papid-envasadoras/internal/config"
	"papid-envasadoras/internal/model"
	"papid-envasadoras/internal/mqttclient"
	"papid-envasadoras/internal/natsclient"
	"papid-envasadoras/internal/persistence"
	"papid-envasadoras/internal/personal"
	"papid-envasadoras/internal/render"
	"papid-envasadoras/internal/sse"
	"papid-envasadoras/internal/store"
)

func main() {
	// config.yaml es la ÚNICA configuración del dashboard: servidor, NATS, MQTT
	// y las valvuladoras. La ruta se puede sobreescribir con CONFIG_YAML (útil
	// en Docker); por defecto "config.yaml" en el directorio de trabajo.
	rutaConfig := getenv("CONFIG_YAML", "config.yaml")
	cfg, err := config.Cargar(rutaConfig)
	if err != nil {
		log.Fatalf("[dashboard] No se pudo cargar %s: %v", rutaConfig, err)
	}

	port := cfg.Servidor.Port
	machineCode := strings.TrimSpace(cfg.Servidor.MachineCode)
	numEnvasadoras := cfg.NumValvuladoras()
	codigos := cfg.Codigos()

	// --- Componentes ---
	st := store.New(machineCode, numEnvasadoras, codigos)
	// Personal EN VIVO (login/logout por tarjeta) que publica el emitter.
	stPersonal := personal.New(cfg.Servidor.SlotsPersonal)
	broker := sse.New()

	renderer, err := render.New("internal/dashboard/web/templates")
	if err != nil {
		log.Fatalf("[dashboard] Error cargando plantillas: %v", err)
	}

	// natsOK refleja si el dashboard tiene conexión viva a NATS. El navegador
	// lo usa para distinguir "En vivo" (hay datos del PLC/emitter) de "Sin
	// datos" (el dashboard está, pero NATS cayó). Arranca en false.
	var natsOK atomic.Bool
	eventoNATS := func() sse.Evento {
		estado := "false"
		if natsOK.Load() {
			estado = "true"
		}
		return sse.Evento{Nombre: "nats", Data: `{"conectado":` + estado + `}`}
	}

	// Al conectar un navegador: le damos el estado actual Y el estado de NATS,
	// para que el footer no quede mal hasta el próximo cambio.
	eventoPersonal := func() sse.Evento {
		return sse.Evento{Nombre: "personal", Data: renderer.PersonalJSON(stPersonal.Slots())}
	}
	broker.SetOnConnect(func() []sse.Evento {
		return []sse.Evento{
			{Nombre: "estado", Data: renderer.EstadoJSON(st.Estado())},
			eventoPersonal(),
			eventoNATS(),
		}
	})
	st.SetOnChange(func() {
		broker.Publicar(sse.Evento{Nombre: "estado", Data: renderer.EstadoJSON(st.Estado())})
	})
	stPersonal.SetOnChange(func() {
		broker.Publicar(eventoPersonal())
	})

	// --- Persistencia (NATS KV) ---
	// El KV se abre al conectar a NATS. Mientras no haya KV, lo que se guarde
	// se descarta; al conectar se guarda el estado actual si hacía falta.
	var kvActual atomic.Pointer[persistence.KV]
	colaKV := make(chan model.Snapshot, 1)
	encolar := func(snap model.Snapshot) {
		// Capacidad 1: el más nuevo reemplaza al pendiente (es el que importa).
		select {
		case colaKV <- snap:
		default:
			select {
			case <-colaKV:
			default:
			}
			select {
			case colaKV <- snap:
			default:
			}
		}
	}
	go func() {
		for snap := range colaKV {
			if kv := kvActual.Load(); kv != nil {
				if err := kv.Guardar(machineCode, snap); err != nil {
					log.Printf("[kv] Error guardando %s: %v", machineCode, err)
				}
			}
		}
	}()
	st.SetOnGuardar(encolar)

	// emitterKV lee (sin escribir) el KV del emitter, para la vista /control.
	var emitterKV atomic.Pointer[persistence.EmitterKV]

	// nc se declara aquí (no dentro del bloque NATS) porque varias closures de
	// arriba lo capturan (publicarOrden, controlEmitter).
	var nc *nats.Conn

	// restaurarPersonal lee del KV del emitter el personal y su status de
	// login. Se llama al conectar y en cada reconexión, para ponerse al día con
	// los login/logout que pasaron mientras no había NATS. Sin clave = el silo
	// no tiene personal asignado (todo vacante).
	restaurarPersonal := func() {
		ekv := emitterKV.Load()
		if ekv == nil {
			return
		}
		personas, existe, err := ekv.CargarPersonal(machineCode)
		if err != nil {
			log.Printf("[kv] Error leyendo el personal del emitter: %v", err)
			return
		}
		if !existe {
			personas = nil
		}
		stPersonal.AplicarSync(personas)
	}

	// Al conectar (la primera vez): abrir el KV y restaurar lo guardado.
	alConectar := func(c *nats.Conn) {
		kv, err := persistence.New(c)
		if err != nil {
			log.Printf("[kv] No disponible: %v (se sigue sin persistencia)", err)
			return
		}
		kvActual.Store(kv)

		// Lector del KV del emitter (puede no existir aún; no es fatal).
		if ekv, err := persistence.NewEmitter(c); err != nil {
			log.Printf("[kv] No se pudo abrir el KV del emitter: %v", err)
		} else if ekv != nil {
			emitterKV.Store(ekv)
		}

		// Personal en vivo: suscripción al emitter + estado inicial del KV
		// (para no esperar al próximo login/logout para pintar las tarjetas).
		if _, err := natsclient.SuscribirPersonal(c, machineCode, stPersonal); err != nil {
			log.Printf("[nats] No se pudo suscribir al personal del emitter: %v", err)
		}
		restaurarPersonal()

		// Solo en PRUEBAS: además escucha el proceso (peso/bultos/leds) por
		// NATS en papid.envasadora.<code>, para poder inyectar desde otra PC.
		// En producción el proceso llega por MQTT; por eso va tras la bandera.
		if os.Getenv("DEBUG_PRUEBAS") == "1" {
			cfgProc := natsclient.Config{MachineCode: machineCode}
			if _, err := natsclient.Suscribir(c, cfgProc, st); err != nil {
				log.Printf("[nats] (pruebas) No se pudo suscribir al proceso: %v", err)
			} else {
				log.Printf("[nats] (pruebas) Suscrito al proceso %s para inyección", cfgProc.Subject())
			}
		}

		snap, err := kv.Cargar(machineCode)
		switch {
		case err != nil:
			log.Printf("[kv] Error leyendo %s: %v", machineCode, err)
		case snap != nil && st.Restaurar(*snap):
			if snap.Orden != nil {
				log.Printf("[kv] Restaurada la orden %s (guardada %s)", snap.Orden.DocNum, snap.Guardado)
			} else {
				log.Printf("[kv] Estado restaurado (sin orden)")
			}
		case st.TieneOrden():
			// Llegó una orden por POST antes de conectar: se guarda ahora.
			encolar(st.Snapshot())
		}

		// Republica el aviso de orden para que Node-RED tenga el estado vigente
		// aunque haya arrancado (o reconectado) después del dashboard.
		if data, err := json.Marshal(st.EventoOrden()); err == nil {
			c.Publish("papid.envasadora.orden."+machineCode, data)
		}
	}

	// --- Aviso de orden a Node-RED (cambio de pantalla) ---
	// Cuando la orden aparece/desaparece, se publica a NATS en
	// papid.envasadora.orden.<MACHINE_CODE>. Node-RED combina ESTO (¿hay orden?)
	// con el personal del emitter (¿hay trabajadores registrados?) para decidir:
	//   orden + personal registrado -> pantalla de envasadoras
	//   falta cualquiera            -> Dashboard Personal
	// nc se asigna más abajo; estas closures lo leen cuando ya está listo.
	subjOrden := "papid.envasadora.orden." + machineCode
	publicarOrden := func(ev store.EventoOrden) {
		if nc == nil {
			return
		}
		if data, err := json.Marshal(ev); err == nil {
			if err := nc.Publish(subjOrden, data); err != nil {
				log.Printf("[dashboard] No se pudo publicar el aviso de orden: %v", err)
			}
		}
	}
	st.SetOnOrden(publicarOrden)

	// --- NATS ---
	cfgNats := natsclient.Config{
		URL:         cfg.NATS.URL,
		User:        cfg.NATS.Usuario,
		Pass:        cfg.NATS.Password,
		MachineCode: machineCode,
	}
	// onEstadoNATS actualiza la bandera y empuja el estado a las pantallas en
	// vivo (sin refresh), para que el footer sepa si hay datos del PLC/emitter.
	onEstadoNATS := func(conectado bool) {
		natsOK.Store(conectado)
		broker.Publicar(eventoNATS())
		// En una RECONEXIÓN el KV del emitter ya está abierto: se relee el
		// personal (en la primera conexión emitterKV aún es nil y lo hace
		// alConectar). Va en goroutine para no bloquear el handler de NATS.
		if conectado {
			go restaurarPersonal()
		}
	}
	if conn, err := natsclient.Conectar(cfgNats, alConectar, onEstadoNATS); err != nil {
		log.Printf("[dashboard] No se pudo conectar a NATS: %v (el dashboard sigue funcionando)", err)
	} else {
		nc = conn
		// NOTA: el dashboard ya NO escucha el proceso por NATS. El peso de las
		// bolas llega por MQTT (ver más abajo). NATS queda solo para el estado
		// de conexión (footer) y el aviso de orden al cambio de pantalla.
	}

	// --- MQTT: peso de las bolas ---
	// Es la fuente del proceso ahora. Se conecta al broker del config.yaml y
	// actualiza el peso de cada bola según su topic. onEstado podría empujarse
	// al footer en el futuro; por ahora solo se registra.
	mqttCli := mqttclient.Nuevo(cfg, st, func(conectado bool) {
		if conectado {
			log.Printf("[dashboard] MQTT conectado (peso de las bolas)")
		} else {
			log.Printf("[dashboard] MQTT desconectado (sin peso de las bolas)")
		}
	})
	mqttCli.Conectar()
	defer mqttCli.Desconectar()

	// --- Reseteo diario ---
	// Al cambio de turno se borra TODO (orden + personal + contadores).
	iniciarResetDiario(cfg.Servidor.ResetHora, func() {
		st.QuitarTodo()
		log.Println("[dashboard] Reseteo diario: orden, personal y contadores en cero")
	})

	// --- HTTP ---
	mux := http.NewServeMux()

	token := cfg.Servidor.APIToken
	if strings.TrimSpace(token) == "" {
		log.Println("[dashboard] AVISO: api_token vacío, POST/DELETE /api/orden quedan SIN autenticación")
	}
	ctrl := &controlEmitter{nc: nc, code: machineCode, emitterKV: &emitterKV}
	api.New(st, token, ctrl).Registrar(mux)

	// --- Endpoint de PRUEBAS (solo si DEBUG_PRUEBAS=1) ---
	// Inyecta peso y bultos directo al store, sin MQTT ni PLC, para probar la
	// UI (p.ej. que un número grande se ajuste y no se desborde). NO se registra
	// en producción. Ejemplos:
	//   GET /debug/proceso?bola=0&peso=123456.7
	//   GET /debug/proceso?bola=1&bultos=99999   (requiere orden cargada)
	if os.Getenv("DEBUG_PRUEBAS") == "1" {
		registrarDebugPruebas(mux, st)
		log.Println("[dashboard] DEBUG_PRUEBAS=1: endpoint /debug/proceso ACTIVO (solo para pruebas)")
	}

	datosIndex := render.DatosIndex{
		Titulo:         construirTitulo(cfg.Servidor.Region, cfg.Servidor.Maquina),
		Maquina:        cfg.Servidor.Maquina,
		Version:        strconv.FormatInt(time.Now().Unix(), 10),
		NumEnvasadoras: numEnvasadoras,
		Codigos:        codigos,

		CarouselSegundos: cfg.Servidor.CarouselSegundos,
		SlotsPersonal:    stPersonal.NumSlots(),
		Opcionales:       make([]render.Opcionales, numEnvasadoras),
	}
	for i, v := range cfg.Valvuladoras {
		datosIndex.Opcionales[i] = render.Opcionales{
			Setpoint: strings.TrimSpace(v.SetpointTopic) != "",
			Tara:     strings.TrimSpace(v.TaraTopic) != "",
			Columna:  strings.TrimSpace(v.ColumnaTopic) != "",
		}
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		html, err := renderer.Index(datosIndex)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(html))
	})
	mux.HandleFunc("GET /events", broker.Handler)
	mux.Handle("GET /static/", http.StripPrefix("/static/",
		http.FileServer(http.Dir("internal/dashboard/web/static"))))

	// Sin WriteTimeout porque cortaría los streams SSE.
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("[dashboard] Silo: %s (%d envasadoras)", machineCode, numEnvasadoras)
	log.Printf("[dashboard] Proceso NATS: %s", cfgNats.Subject())
	log.Printf("[dashboard] Corriendo en http://localhost:%s  ·  docs en /docs", port)

	errSrv := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errSrv <- err
		}
	}()

	// --- Apagado ordenado ---
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errSrv:
		log.Printf("[dashboard] Error en el servidor: %v", err)
	case <-sig:
		log.Println("[dashboard] Apagando...")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("[dashboard] Cierre del servidor incompleto: %v", err)
	}
	// Último guardado antes de salir.
	if kv := kvActual.Load(); kv != nil {
		if err := kv.Guardar(machineCode, st.Snapshot()); err != nil {
			log.Printf("[kv] Error en el guardado final: %v", err)
		}
	}
	if nc != nil {
		if err := nc.Drain(); err != nil {
			log.Printf("[dashboard] Drain de NATS incompleto: %v", err)
		}
	}
	log.Println("[dashboard] Detenido")
}

// registrarDebugPruebas monta GET /debug/proceso para inyectar peso y bultos
// a mano (solo pruebas). peso usa AplicarCampo (no depende de orden); bultos
// usa AplicarProceso con el contador del PLC (solo cuenta si hay orden).
func registrarDebugPruebas(mux *http.ServeMux, st *store.Store) {
	mux.HandleFunc("GET /debug/proceso", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		bola, _ := strconv.Atoi(q.Get("bola")) // default 0

		if v := q.Get("peso"); v != "" {
			peso, err := strconv.ParseFloat(v, 64)
			if err != nil {
				http.Error(w, "peso inválido: "+err.Error(), http.StatusBadRequest)
				return
			}
			st.AplicarCampo(bola, store.CampoPeso, peso)
		}
		if v := q.Get("setpoint"); v != "" {
			f, _ := strconv.ParseFloat(v, 64)
			st.AplicarCampo(bola, store.CampoSetpoint, f)
		}
		if v := q.Get("tara"); v != "" {
			f, _ := strconv.ParseFloat(v, 64)
			st.AplicarCampo(bola, store.CampoTara, f)
		}
		if v := q.Get("columna"); v != "" {
			f, _ := strconv.ParseFloat(v, 64)
			st.AplicarCampo(bola, store.CampoColumna, f)
		}

		// bultos: el dashboard cuenta los INCREMENTOS del contador del PLC y
		// solo si hay orden. Para fijar un valor exacto mandamos dos lecturas:
		// primero 0 (línea base) y luego n (el incremento = n). Así el contador
		// de la orden de esa bola queda en n. Requiere una orden cargada.
		if v := q.Get("bultos"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				http.Error(w, "bultos inválido: "+err.Error(), http.StatusBadRequest)
				return
			}
			nv := st.NumEnvasadoras()
			base := model.EstadoVacio(nv)
			st.AplicarProceso(base) // línea base: contador en 0
			if bola >= 0 && bola < nv {
				base.Valvuladoras[bola].Bultos = n
			}
			st.AplicarProceso(base) // incremento n -> bultos de la orden = n
		}
		w.Write([]byte("ok\n"))
	})
}

// iniciarResetDiario ejecuta fn todos los días a la hora "HH:MM" (hora local,
// TZ). Vacío u "off" lo desactiva.
func iniciarResetDiario(hora string, fn func()) {
	hora = strings.TrimSpace(hora)
	if hora == "" || strings.EqualFold(hora, "off") {
		log.Println("[dashboard] Reseteo diario desactivado (RESET_HORA vacío)")
		return
	}
	t, err := time.Parse("15:04", hora)
	if err != nil {
		log.Printf("[dashboard] RESET_HORA inválido (%q, formato HH:MM): reseteo desactivado", hora)
		return
	}
	log.Printf("[dashboard] Reseteo diario a las %s", t.Format("15:04"))
	go func() {
		for {
			ahora := time.Now()
			prox := time.Date(ahora.Year(), ahora.Month(), ahora.Day(), t.Hour(), t.Minute(), 0, 0, ahora.Location())
			if !prox.After(ahora) {
				prox = prox.AddDate(0, 0, 1)
			}
			time.Sleep(time.Until(prox))
			fn()
		}
	}()
}

// construirTitulo arma "Envasadoras | REGION | MAQUINA" con los valores del
// config.yaml. La región va en mayúsculas, igual que antes.
func construirTitulo(region, maquina string) string {
	partes := []string{"Envasadoras"}
	if region != "" {
		partes = append(partes, strings.ToUpper(region))
	}
	if maquina != "" {
		partes = append(partes, maquina)
	}
	return strings.Join(partes, " | ")
}

// getenv devuelve la variable de entorno o el default. Se usa solo para
// CONFIG_YAML (la ruta del archivo de configuración).
func getenv(clave, def string) string {
	if v := os.Getenv(clave); v != "" {
		return v
	}
	return def
}

// controlEmitter implementa api.Control: lee la orden del emitter desde su KV
// y le publica el unsigned para quitarla. Es el puente entre el dashboard y el
// emitter para la vista /control.
type controlEmitter struct {
	nc        *nats.Conn
	code      string
	emitterKV *atomic.Pointer[persistence.EmitterKV]
}

// OrdenEmitter devuelve la orden del emitter de este silo. El segundo valor es
// false si el emitter no está accesible (sin NATS o sin su bucket).
func (c *controlEmitter) OrdenEmitter() (any, bool) {
	kv := c.emitterKV.Load()
	if kv == nil {
		return nil, false
	}
	o, err := kv.Cargar(c.code)
	if err != nil {
		log.Printf("[control] Error leyendo la orden del emitter: %v", err)
		return nil, true // el KV existe, pero esta máquina no tiene orden/dio error
	}
	return o, true // o puede ser nil (sin orden), pero el emitter sí responde
}

// QuitarOrdenEmitter publica papid.admin.unsigned para que el emitter borre la
// orden y el personal de este silo (igual que en las a2i).
func (c *controlEmitter) QuitarOrdenEmitter() error {
	if c.nc == nil {
		return os.ErrClosed
	}
	payload := []byte(`{"machine_code":"` + c.code + `","personnel":[]}`)
	if err := c.nc.Publish("papid.admin.unsigned", payload); err != nil {
		return err
	}
	return c.nc.FlushTimeout(3 * time.Second)
}

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

	"github.com/joho/godotenv"
	"github.com/nats-io/nats.go"

	"papid-envasadoras/internal/api"
	"papid-envasadoras/internal/model"
	"papid-envasadoras/internal/natsclient"
	"papid-envasadoras/internal/persistence"
	"papid-envasadoras/internal/render"
	"papid-envasadoras/internal/sse"
	"papid-envasadoras/internal/store"
)

func main() {
	// Carga el .env (si existe); en Docker las variables llegan por el entorno.
	if err := godotenv.Overload(); err != nil {
		log.Println("[dashboard] No se encontró .env, se usan variables del entorno")
	}

	port := getenv("PORT", "3000")
	machineCode := strings.TrimSpace(os.Getenv("MACHINE_CODE"))
	if machineCode == "" || strings.ContainsAny(machineCode, " \t*>") {
		log.Fatalf("[dashboard] MACHINE_CODE inválido o vacío (%q). Ej: B2-A-silo-1", machineCode)
	}

	// Número de envasadoras (bolas) del silo: 2 en silos 1 y 4, 3 en 2 y 3.
	numEnvasadoras := model.DefaultEnvasadoras
	if v := os.Getenv("NUM_ENVASADORAS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			numEnvasadoras = n
		} else {
			log.Printf("[dashboard] NUM_ENVASADORAS inválido (%q), se usa %d", v, numEnvasadoras)
		}
	}

	// Código identificador de cada bola (CODIGOS_ENVASADORAS del .env), en
	// orden. Solo es informativo (se muestra junto a los bultos y viaja en el
	// estado para reportes). Se ajusta a numEnvasadoras.
	codigos := codigosEnvasadoras(os.Getenv("CODIGOS_ENVASADORAS"), numEnvasadoras)

	// --- Componentes ---
	st := store.New(machineCode, numEnvasadoras, codigos)
	broker := sse.New()

	renderer, err := render.New("internal/dashboard/web/templates")
	if err != nil {
		log.Fatalf("[dashboard] Error cargando plantillas: %v", err)
	}

	broker.SetOnConnect(func() sse.Evento {
		return sse.Evento{Nombre: "estado", Data: renderer.EstadoJSON(st.Estado())}
	})
	st.SetOnChange(func() {
		broker.Publicar(sse.Evento{Nombre: "estado", Data: renderer.EstadoJSON(st.Estado())})
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
		URL:         getenv("NATS_URL", "nats://localhost:4222"),
		User:        os.Getenv("NATS_USER"),
		Pass:        os.Getenv("NATS_PASS"),
		MachineCode: machineCode,
	}
	if conn, err := natsclient.Conectar(cfgNats, alConectar); err != nil {
		log.Printf("[dashboard] No se pudo conectar a NATS: %v (el dashboard sigue funcionando)", err)
	} else {
		nc = conn
		if _, err := natsclient.Suscribir(nc, cfgNats, st); err != nil {
			log.Printf("[dashboard] No se pudo suscribir a NATS: %v", err)
		}
	}

	// --- Reseteo diario ---
	iniciarResetDiario(os.Getenv("RESET_HORA"), func() {
		st.QuitarOrden()
		log.Println("[dashboard] Reseteo diario: orden, personal y contadores en cero")
	})

	// --- HTTP ---
	mux := http.NewServeMux()

	token := os.Getenv("API_TOKEN")
	if strings.TrimSpace(token) == "" {
		log.Println("[dashboard] AVISO: API_TOKEN vacío, POST/DELETE /api/orden quedan SIN autenticación")
	}
	ctrl := &controlEmitter{nc: nc, code: machineCode, emitterKV: &emitterKV}
	api.New(st, token, ctrl).Registrar(mux)

	datosIndex := render.DatosIndex{
		Titulo:         construirTitulo(),
		Maquina:        getenv("MAQUINA", machineCode),
		Version:        strconv.FormatInt(time.Now().Unix(), 10),
		NumEnvasadoras: numEnvasadoras,
		Codigos:        codigos,
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

// construirTitulo arma "Envasadoras | REGION | MAQUINA".
func construirTitulo() string {
	partes := []string{"Envasadoras"}
	if region := os.Getenv("REGION"); region != "" {
		partes = append(partes, strings.ToUpper(region))
	}
	if maquina := os.Getenv("MAQUINA"); maquina != "" {
		partes = append(partes, maquina)
	}
	return strings.Join(partes, " | ")
}

// codigosEnvasadoras parte CODIGOS_ENVASADORAS (lista separada por comas) y
// devuelve exactamente n códigos, en orden. Si llegan menos, los faltantes
// quedan en ""; si llegan más, se descartan los sobrantes. Es solo informativo:
// el código de cada bola se muestra junto a los bultos y viaja en el estado.
func codigosEnvasadoras(raw string, n int) []string {
	out := make([]string, n)
	if n <= 0 {
		return out
	}
	i := 0
	for _, parte := range strings.Split(raw, ",") {
		if i >= n {
			break
		}
		c := strings.TrimSpace(parte)
		if c == "" {
			continue
		}
		out[i] = c
		i++
	}
	return out
}

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

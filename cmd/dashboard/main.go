// Dashboard de Envasadoras — Go + SSE + NATS (solo frontend/consumidor).
//
// Flujo:
//   - NATS empuja el estado de la envasadora en papid.envasadora.<MACHINE_CODE>.
//   - El store lo mantiene en memoria (OF, item, lote y las 3 valvuladoras).
//   - Cuando el estado cambia, se serializa a JSON y se empuja a los
//     navegadores por SSE (evento "estado").
//   - El navegador dibuja las tres bolas (ECharts liquidFill) y actualiza las
//     etiquetas sin recargar la página.
//
// Este servicio NO publica nada a NATS. Setpoint y columna se cambian desde
// Node-RED y llegan ya modificados en el mensaje; aquí solo se muestran.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/nats-io/nats.go"

	"papid-envasadoras/internal/natsclient"
	"papid-envasadoras/internal/render"
	"papid-envasadoras/internal/sse"
	"papid-envasadoras/internal/store"
)

func main() {
	// Carga el .env (si existe). Overload hace que el .env GANE sobre las
	// variables ya presentes en el entorno. No es fatal si falta.
	if err := godotenv.Overload(); err != nil {
		log.Println("[dashboard] No se encontró .env, se usan variables del entorno")
	}

	port := getenv("PORT", "3000")
	machineCode := os.Getenv("MACHINE_CODE")

	// --- Componentes ---
	st := store.New(machineCode)
	broker := sse.New()

	renderer, err := render.New("internal/dashboard/web/templates")
	if err != nil {
		log.Fatalf("[dashboard] Error cargando plantillas: %v", err)
	}

	// Cuando un nuevo navegador se conecta por SSE, le enviamos el estado
	// actual de inmediato para que no arranque en blanco.
	broker.SetOnConnect(func() sse.Evento {
		return sse.Evento{Nombre: "estado", Data: renderer.EstadoJSON(st.Estado())}
	})

	// Cuando el estado cambia (llegó algo por NATS), empujamos el JSON por SSE.
	st.SetOnChange(func() {
		broker.Publicar(sse.Evento{Nombre: "estado", Data: renderer.EstadoJSON(st.Estado())})
	})

	// --- Conexión a NATS ---
	cfgNats := natsclient.Config{
		URL:         getenv("NATS_URL", "nats://localhost:4222"),
		User:        os.Getenv("NATS_USER"),
		Pass:        os.Getenv("NATS_PASS"),
		MachineCode: machineCode,
	}

	var nc *nats.Conn
	if conn, err := natsclient.Conectar(cfgNats); err != nil {
		log.Printf("[dashboard] No se pudo conectar a NATS: %v (el dashboard sigue funcionando)", err)
	} else {
		nc = conn
		if _, err := natsclient.Suscribir(nc, cfgNats, st); err != nil {
			log.Printf("[dashboard] No se pudo suscribir a NATS: %v", err)
		}
	}

	// --- Rutas HTTP ---
	mux := http.NewServeMux()

	titulo := construirTitulo()
	datosIndex := render.DatosIndex{
		Titulo:  titulo,
		Maquina: getenv("MAQUINA", machineCode),
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		html, err := renderer.Index(datosIndex)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(html))
	})

	// Stream SSE.
	mux.HandleFunc("/events", broker.Handler)

	// Archivos estáticos (CSS, fuentes, vendor de ECharts, logo).
	mux.Handle("/static/", http.StripPrefix("/static/",
		http.FileServer(http.Dir("internal/dashboard/web/static"))))

	// Servidor con timeouts explícitos. Sin WriteTimeout porque cortaría los
	// streams SSE; el plazo de escritura se aplica por evento en el broker.
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("[dashboard] Envasadora: %s", machineCode)
	log.Printf("[dashboard] Subject NATS: %s", cfgNats.Subject())
	log.Printf("[dashboard] Corriendo en http://localhost:%s", port)

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
	if nc != nil {
		if err := nc.Drain(); err != nil {
			log.Printf("[dashboard] Drain de NATS incompleto: %v", err)
		}
	}
	log.Println("[dashboard] Detenido")
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

func getenv(clave, def string) string {
	if v := os.Getenv(clave); v != "" {
		return v
	}
	return def
}

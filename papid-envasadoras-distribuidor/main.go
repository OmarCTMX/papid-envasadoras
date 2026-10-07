// Distribuidor de proceso (envasadoras).
//
// Objetivo: UNA sola conexión al PLC. Node-RED (con un solo nodo S7) lee todo
// el PLC y publica un mensaje con el proceso de TODAS las envasadoras en:
//
//	papid.envasadoras.plc        (el "blob")
//
// El distribuidor recibe ese blob, lo PARTE por máquina y publica el estado de
// cada envasadora en su subject, que es justo lo que consume cada dashboard:
//
//	papid.envasadora.<machine_code>
//
// Así el PLC solo tiene UNA conexión (Node-RED) y las N envasadoras/dashboards
// se alimentan desde NATS, sin tocar al PLC. Es escalable: agregar una máquina
// no requiere cambios, solo que venga en el blob.
//
// Además guarda el último estado de cada envasadora en NATS KV, para que un
// dashboard que arranque tarde vea datos sin esperar al PLC.
//
// Uso:  go run .
package main

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/nats-io/nats.go"

	"papid-envasadoras-distribuidor/internal/model"
	"papid-envasadoras-distribuidor/internal/persistence"
)

const (
	// subjBlob: entrada. Node-RED publica aquí el proceso de TODAS las máquinas.
	subjBlob = "papid.envasadoras.plc"
	// prefijoSalida + machine_code: lo que escucha cada dashboard.
	prefijoSalida = "papid.envasadora."
)

func main() {
	if err := godotenv.Overload(); err != nil {
		log.Println("[distribuidor] No se encontró .env, se usan variables del entorno")
	}

	subBlob := getenv("SUBJECT_BLOB", subjBlob)
	log.Printf("[distribuidor] Escucha el blob del PLC en: %s", subBlob)

	nc := conectar()
	defer nc.Drain()

	// KV (opcional): guarda el último estado de cada envasadora.
	var kv *persistence.KV
	if k, err := persistence.New(nc); err != nil {
		log.Printf("[distribuidor] KV no disponible: %v (se reparte sin persistencia)", err)
	} else {
		kv = k
		log.Printf("[distribuidor] KV listo (bucket de proceso por envasadora)")
	}

	// pub serializa y publica, registrando errores.
	pub := func(subject string, v any) {
		data, err := json.Marshal(v)
		if err != nil {
			log.Printf("[distribuidor] Error serializando para %s: %v", subject, err)
			return
		}
		if err := nc.Publish(subject, data); err != nil {
			log.Printf("[distribuidor] Error publicando en %s: %v", subject, err)
		}
	}

	// repartir toma el blob, publica cada envasadora en su subject y la guarda
	// en el KV. Ignora las que no traigan machine_code (no se sabría a dónde).
	repartir := func(envs []model.Envasadora) {
		publicadas := 0
		for _, e := range envs {
			code := strings.TrimSpace(e.MachineCode)
			if code == "" {
				log.Printf("[distribuidor] Envasadora sin machine_code en el blob (ignorada)")
				continue
			}
			pub(prefijoSalida+code, e)
			if kv != nil {
				if err := kv.Guardar(e); err != nil {
					log.Printf("[distribuidor] Error guardando %s en KV: %v", code, err)
				}
			}
			publicadas++
		}
		if publicadas > 0 {
			log.Printf("[distribuidor] Repartidas %d envasadoras", publicadas)
		}
	}

	// Suscripción al blob del PLC.
	sub, err := nc.Subscribe(subBlob, func(m *nats.Msg) {
		envs, err := decodBlob(m.Data)
		if err != nil {
			log.Printf("[distribuidor] Blob inválido en %s (ignorado): %v", subBlob, err)
			return
		}
		repartir(envs)
	})
	if err != nil {
		log.Fatalf("[distribuidor] No se pudo suscribir a %s: %v", subBlob, err)
	}
	// Buffer amplio: el PLC puede publicar seguido.
	if err := sub.SetPendingLimits(65536, 128*1024*1024); err != nil {
		log.Printf("[distribuidor] No se pudieron ajustar los límites de la suscripción: %v", err)
	}

	log.Println("[distribuidor] Corriendo. Ctrl+C para detener.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("[distribuidor] Apagando...")
	if err := nc.FlushTimeout(5 * time.Second); err != nil {
		log.Printf("[distribuidor] Flush incompleto al apagar: %v", err)
	}
}

// decodBlob acepta las dos formas del blob:
//   - objeto con { "envasadoras": [ ... ] }
//   - arreglo directo [ ... ]
//
// Así Node-RED puede mandar cualquiera de las dos sin romper nada.
func decodBlob(data []byte) ([]model.Envasadora, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var arr []model.Envasadora
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, err
		}
		return arr, nil
	}
	var blob model.Blob
	if err := json.Unmarshal(trimmed, &blob); err != nil {
		return nil, err
	}
	return blob.Envasadoras, nil
}

func conectar() *nats.Conn {
	opts := []nats.Option{
		nats.Name("papid-envasadoras-distribuidor"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.ReconnectJitter(100*time.Millisecond, time.Second),
		nats.Timeout(10 * time.Second),
		nats.RetryOnFailedConnect(true),
		nats.ReconnectBufSize(16 * 1024 * 1024),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			log.Printf("[distribuidor] NATS desconectado: %v (reintentando...)", err)
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			log.Printf("[distribuidor] NATS reconectado a %s", c.ConnectedUrl())
		}),
		nats.ClosedHandler(func(_ *nats.Conn) {
			log.Println("[distribuidor] NATS: conexión cerrada definitivamente")
		}),
		nats.ErrorHandler(func(_ *nats.Conn, s *nats.Subscription, err error) {
			subject := ""
			if s != nil {
				subject = s.Subject
			}
			log.Printf("[distribuidor] NATS error (subject %q): %v", subject, err)
		}),
	}
	if u := os.Getenv("NATS_USER"); u != "" {
		opts = append(opts, nats.UserInfo(u, os.Getenv("NATS_PASS")))
	}
	nc, err := nats.Connect(getenv("NATS_URL", "nats://localhost:4222"), opts...)
	if err != nil {
		log.Fatalf("[distribuidor] No se pudo conectar a NATS: %v", err)
	}
	log.Printf("[distribuidor] Conectado a NATS en %s", nc.ConnectedUrl())
	return nc
}

func getenv(clave, def string) string {
	if v := os.Getenv(clave); v != "" {
		return v
	}
	return def
}

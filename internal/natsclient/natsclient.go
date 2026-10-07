// Package natsclient se conecta a NATS y escucha el PROCESO de este silo en
// papid.envasadora.<MACHINE_CODE> (lo que manda el PLC vía Node-RED o el
// distribuidor): peso de cada bola, contador de bultos, setpoint, columna y
// LEDs.
//
// La orden y el personal NO llegan por aquí: llegan por POST /api/orden.
// Este paquete es SOLO CONSUMIDOR: no publica nada a NATS.
package natsclient

import (
	"encoding/json"
	"log"
	"time"

	"github.com/nats-io/nats.go"

	"papid-envasadoras/internal/model"
	"papid-envasadoras/internal/store"
)

// subjectPrefix + MACHINE_CODE forma el subject de proceso de este silo.
const subjectPrefix = "papid.envasadora."

// Config con los datos de conexión.
type Config struct {
	URL         string
	User        string
	Pass        string
	MachineCode string // silo de ESTE dashboard
}

// Subject devuelve el subject de proceso que escucha este dashboard.
func (c Config) Subject() string {
	return subjectPrefix + c.MachineCode
}

// Conectar abre la conexión a NATS (sin suscribirse todavía).
//
// onConectado se llama UNA vez, cuando la primera conexión se establece
// (de inmediato o más tarde si NATS no estaba disponible al arrancar). Sirve
// para restaurar el estado guardado en el KV.
func Conectar(cfg Config, onConectado func(*nats.Conn)) (*nats.Conn, error) {
	opts := []nats.Option{
		nats.Name("dashboard-envasadoras"),
		// Servicio 24/7: reintentar la reconexión INDEFINIDAMENTE.
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.ReconnectJitter(100*time.Millisecond, time.Second),
		nats.Timeout(10 * time.Second),
		// Permite arrancar aunque NATS todavía no esté disponible.
		nats.RetryOnFailedConnect(true),
		nats.ConnectHandler(func(c *nats.Conn) {
			log.Printf("[nats] Conectado a %s", c.ConnectedUrl())
			if onConectado != nil {
				onConectado(c)
			}
		}),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			log.Printf("[nats] Desconectado: %v (reintentando...)", err)
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			log.Printf("[nats] Reconectado a %s", c.ConnectedUrl())
		}),
		nats.ClosedHandler(func(_ *nats.Conn) {
			log.Println("[nats] Conexión cerrada definitivamente")
		}),
		nats.ErrorHandler(func(_ *nats.Conn, s *nats.Subscription, err error) {
			subject := ""
			if s != nil {
				subject = s.Subject
			}
			log.Printf("[nats] Error (subject %q): %v", subject, err)
		}),
	}
	if cfg.User != "" {
		opts = append(opts, nats.UserInfo(cfg.User, cfg.Pass))
	}
	return nats.Connect(cfg.URL, opts...)
}

// Suscribir escucha el proceso de este silo y lo aplica al store.
func Suscribir(nc *nats.Conn, cfg Config, st *store.Store) (*nats.Subscription, error) {
	subject := cfg.Subject()
	sub, err := nc.Subscribe(subject, func(m *nats.Msg) {
		var estado model.Estado
		if err := json.Unmarshal(m.Data, &estado); err != nil {
			log.Printf("[nats] Mensaje inválido en %s (ignorado): %v", subject, err)
			return
		}
		// El subject ya es de este silo, pero si el mensaje trae machine_code
		// y no coincide, se descarta por seguridad.
		if estado.MachineCode != "" && estado.MachineCode != cfg.MachineCode {
			log.Printf("[nats] Descartado: machine_code=%q no es %q", estado.MachineCode, cfg.MachineCode)
			return
		}
		st.AplicarProceso(estado)
	})
	if err != nil {
		return nil, err
	}
	// Buffer amplio: el PLC puede publicar seguido.
	if err := sub.SetPendingLimits(65536, 128*1024*1024); err != nil {
		log.Printf("[nats] No se pudieron ajustar los límites de la suscripción: %v", err)
	}
	log.Printf("[nats] Suscrito a %s", subject)
	return sub, nil
}

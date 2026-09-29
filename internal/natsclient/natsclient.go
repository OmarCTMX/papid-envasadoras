// Package natsclient se conecta a NATS y escucha el estado de ESTA envasadora
// en el subject papid.envasadora.<MACHINE_CODE>.
//
// Este paquete es SOLO CONSUMIDOR: no publica nada a NATS. Los valores
// (peso, setpoint, columna, orden y lote) llegan ya calculados desde el
// productor (Node-RED / PLC) y el dashboard solo los refleja.
package natsclient

import (
	"encoding/json"
	"log"
	"time"

	"github.com/nats-io/nats.go"

	"papid-envasadoras/internal/model"
	"papid-envasadoras/internal/store"
)

// subjectPrefix + MACHINE_CODE forma el subject de esta envasadora.
const subjectPrefix = "papid.envasadora."

// Config con los datos de conexión.
type Config struct {
	URL         string
	User        string
	Pass        string
	MachineCode string // envasadora de ESTE dashboard
}

// Subject devuelve el subject que escucha este dashboard.
func (c Config) Subject() string {
	return subjectPrefix + c.MachineCode
}

// Conectar abre la conexión a NATS (sin suscribirse todavía).
// La suscripción se activa aparte con Suscribir, para que el dashboard pueda
// terminar de inicializarse antes de recibir el primer mensaje.
func Conectar(cfg Config) (*nats.Conn, error) {
	opts := []nats.Option{
		nats.Name("dashboard-envasadoras"),
		// Servicio 24/7: reintentar la reconexión INDEFINIDAMENTE. Con el
		// default (60 intentos) una caída del broker de ~2 min dejaba la
		// conexión cerrada para siempre y el dashboard congelado sin avisar.
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.ReconnectJitter(100*time.Millisecond, time.Second),
		nats.Timeout(10 * time.Second),
		// Permite arrancar aunque NATS todavía no esté disponible.
		nats.RetryOnFailedConnect(true),
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

	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, err
	}
	log.Printf("[nats] Conectado a %s", nc.ConnectedUrl())
	return nc, nil
}

// Suscribir activa la escucha del subject de esta envasadora y aplica cada
// mensaje al store. Devuelve la suscripción por si se quiere cancelar.
func Suscribir(nc *nats.Conn, cfg Config, st *store.Store) (*nats.Subscription, error) {
	subject := cfg.Subject()
	sub, err := nc.Subscribe(subject, func(m *nats.Msg) {
		var estado model.Estado
		if err := json.Unmarshal(m.Data, &estado); err != nil {
			log.Printf("[nats] Mensaje inválido en %s (ignorado): %v", subject, err)
			return
		}

		// El subject ya es específico de esta envasadora, pero si el mensaje
		// trae machine_code y no coincide, lo descartamos por seguridad.
		if estado.MachineCode != "" && estado.MachineCode != cfg.MachineCode {
			log.Printf("[nats] Descartado: machine_code=%q no es %q", estado.MachineCode, cfg.MachineCode)
			return
		}

		log.Printf("[nats] Estado recibido | OF=%q lote=%d/%d valvuladoras=%d",
			estado.OF, estado.LoteActual, estado.LotesTotales, len(estado.Valvuladoras))
		st.Aplicar(estado)
	})
	if err != nil {
		return nil, err
	}
	// Buffer amplio de pendientes: evita descartes por "slow consumer" si
	// llega un pico de mensajes (el productor puede publicar seguido).
	if err := sub.SetPendingLimits(65536, 128*1024*1024); err != nil {
		log.Printf("[nats] No se pudieron ajustar los límites de la suscripción: %v", err)
	}
	log.Printf("[nats] Suscrito a %s", subject)
	return sub, nil
}

// Package mqttclient conecta al broker MQTT (del config.yaml) y escucha el
// peso de cada bola. De cada mensaje solo se usa el campo "value"; el topic ya
// identifica a qué valvuladora va (el mapeo topic→índice sale del YAML).
//
// Hoy MQTT solo trae el peso. En el futuro vendrán más topics (bultos,
// setpoint, LEDs); se agregarán aquí sin tocar el resto del dashboard.
package mqttclient

import (
	"encoding/json"
	"log"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"papid-envasadoras/internal/config"
	"papid-envasadoras/internal/store"
)

// mensajePeso es lo que publica el broker en el topic de cada valvuladora.
// Solo nos interesa Value; el resto se ignora.
//
//	{"name":"b2-b-v1","quality":"good","timestamp":"...","value":20}
type mensajePeso struct {
	Value float64 `json:"value"`
}

// Cliente mantiene la conexión MQTT y enruta cada topic a su bola.
type Cliente struct {
	cli        mqtt.Client
	st         *store.Store
	brokerURL  string         // para logs
	topicABola map[string]int // peso_topic -> índice de bola
	onEstado   func(bool)     // avisa conectado/desconectado (para el footer)
}

// Nuevo arma el cliente a partir de la config y el store. onEstado puede ser
// nil; si no, se llama con true/false al conectar/desconectar.
func Nuevo(cfg *config.Config, st *store.Store, onEstado func(bool)) *Cliente {
	c := &Cliente{
		st:         st,
		brokerURL:  cfg.BrokerURL(),
		topicABola: make(map[string]int, len(cfg.Valvuladoras)),
		onEstado:   onEstado,
	}
	for i, v := range cfg.Valvuladoras {
		c.topicABola[v.PesoTopic] = i
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(cfg.BrokerURL())
	opts.SetClientID("dashboard-envasadoras-" + randSuffix())
	if cfg.MQTT.Usuario != "" {
		opts.SetUsername(cfg.MQTT.Usuario)
		opts.SetPassword(cfg.MQTT.Password)
	}
	// Servicio 24/7: reconexión automática indefinida.
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(2 * time.Second)
	opts.SetMaxReconnectInterval(10 * time.Second)
	opts.SetKeepAlive(20 * time.Second)
	opts.SetCleanSession(true)

	opts.SetOnConnectHandler(func(cli mqtt.Client) {
		log.Printf("[mqtt] Conectado a %s", cfg.BrokerURL())
		c.avisar(true)
		c.suscribir() // (re)suscribe en cada (re)conexión
	})
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		log.Printf("[mqtt] Conexión perdida: %v (reintentando...)", err)
		c.avisar(false)
	})

	c.cli = mqtt.NewClient(opts)
	return c
}

// Conectar inicia la conexión (no bloquea: con ConnectRetry reintenta solo).
func (c *Cliente) Conectar() {
	// Con ConnectRetry, paho reintenta EN SILENCIO si el broker no está: no
	// dispara OnConnect ni ConnectionLost hasta que logra conectar. Para no
	// quedarse a ciegas, avisamos que está intentando.
	log.Printf("[mqtt] Conectando a %s (reintenta solo si no responde)...", c.brokerURL)
	// Connect() con ConnectRetry no bloquea esperando al broker; el token se
	// completa cuando conecta. No se espera aquí para no trabar el arranque.
	c.cli.Connect()
}

// Desconectar cierra la conexión limpiamente (al apagar el dashboard).
func (c *Cliente) Desconectar() {
	if c.cli != nil && c.cli.IsConnected() {
		c.cli.Disconnect(500)
	}
}

// suscribir se suscribe a todos los topics de peso. Se llama en cada conexión
// (los brokers no conservan las suscripciones tras una reconexión con clean
// session).
func (c *Cliente) suscribir() {
	for topic, idx := range c.topicABola {
		i := idx // captura por valor para la closure
		t := topic
		token := c.cli.Subscribe(t, 0, func(_ mqtt.Client, m mqtt.Message) {
			c.manejar(i, m.Payload())
		})
		// No se espera el token de forma bloqueante por cada uno; se registra
		// el error si lo hubo al terminar.
		go func() {
			token.Wait()
			if err := token.Error(); err != nil {
				log.Printf("[mqtt] No se pudo suscribir a %s: %v", t, err)
			} else {
				log.Printf("[mqtt] Suscrito a %s (bola %d)", t, i+1)
			}
		}()
	}
}

// manejar parsea el payload y actualiza el peso de la bola i.
func (c *Cliente) manejar(i int, payload []byte) {
	var msg mensajePeso
	if err := json.Unmarshal(payload, &msg); err != nil {
		log.Printf("[mqtt] Payload inválido para bola %d (ignorado): %v", i+1, err)
		return
	}
	c.st.AplicarPeso(i, msg.Value)
}

func (c *Cliente) avisar(conectado bool) {
	if c.onEstado != nil {
		c.onEstado(conectado)
	}
}

// randSuffix evita que dos dashboards choquen con el mismo client-id si por
// error apuntan al mismo broker. Usa el reloj con nanosegundos.
func randSuffix() string {
	return time.Now().Format("150405.000000000")
}

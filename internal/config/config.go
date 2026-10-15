// Package config carga config.yaml: la configuración del broker MQTT y la
// lista de valvuladoras (bolas) de este silo.
//
// El YAML es la ÚNICA fuente de verdad de las bolas: cuántas hay (el largo de
// la lista), su código (para mostrar en pantalla y los reportes) y de qué topic
// MQTT sale su peso. Antes esto estaba repartido en el .env (NUM_ENVASADORAS,
// CODIGOS_ENVASADORAS); ahora todo vive aquí.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Servidor es la configuración general del dashboard (antes en el .env).
type Servidor struct {
	Port        string `yaml:"port"`         // puerto web (default 3000)
	MachineCode string `yaml:"machine_code"` // código EXACTO del silo (obligatorio)
	Region      string `yaml:"region"`       // región (título/header)
	Maquina     string `yaml:"maquina"`      // nombre visible (footer/título)
	ResetHora   string `yaml:"reset_hora"`   // HH:MM del reseteo diario; vacío/off = sin reseteo
	APIToken    string `yaml:"api_token"`    // token del POST/DELETE; vacío = API abierta
	// CarouselSegundos: cuánto dura cada pantalla del carrusel (default 10).
	CarouselSegundos int `yaml:"carousel_segundos"`
	// SlotsPersonal: cuántos rectángulos de personal muestra la pantalla de
	// personal (1 a 3, default 2). Ej: silos 1/4 = 2 trabajadores, 2/3 = 1.
	SlotsPersonal int `yaml:"slots_personal"`
}

// NATS es la conexión a NATS (solo para el estado de conexión y el aviso de
// orden al cambio de pantalla; el proceso de las bolas ya viene por MQTT).
type NATS struct {
	URL      string `yaml:"url"`      // ej. nats://192.168.90.156:4222
	Usuario  string `yaml:"usuario"`  // vacío si no requiere auth
	Password string `yaml:"password"` // vacío si no requiere auth
}

// MQTT es la configuración del broker (del que llega el peso de las bolas).
type MQTT struct {
	Broker   string `yaml:"broker"`   // IP o host del broker (ej. 10.0.1.176)
	Puerto   int    `yaml:"puerto"`   // puerto MQTT (default 1883)
	Usuario  string `yaml:"usuario"`  // vacío si no requiere auth
	Password string `yaml:"password"` // vacío si no requiere auth
	TLS      bool   `yaml:"tls"`      // true = mqtts
}

// Valvuladora es una bola del silo, en orden (bola 1, bola 2, ...).
type Valvuladora struct {
	Codigo    string `yaml:"codigo"`     // identificador propio (se muestra; reportes)
	PesoTopic string `yaml:"peso_topic"` // topic MQTT del que sale su peso (campo value)
	// Opcionales: si el PLC los publica (mismo formato {"value": ...}). Si se
	// dejan vacíos, la pantalla muestra "—" para ese dato.
	SetpointTopic string `yaml:"setpoint_topic"`
	TaraTopic     string `yaml:"tara_topic"`
	ColumnaTopic  string `yaml:"columna_topic"`
}

// Config es el contenido completo de config.yaml.
type Config struct {
	Servidor     Servidor      `yaml:"servidor"`
	NATS         NATS          `yaml:"nats"`
	MQTT         MQTT          `yaml:"mqtt"`
	Valvuladoras []Valvuladora `yaml:"valvuladoras"`
}

// Cargar lee y valida config.yaml desde la ruta dada.
func Cargar(ruta string) (*Config, error) {
	data, err := os.ReadFile(ruta)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer %s: %w", ruta, err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("YAML inválido en %s: %w", ruta, err)
	}
	if err := c.validar(); err != nil {
		return nil, fmt.Errorf("config inválida en %s: %w", ruta, err)
	}
	// Defaults (mismos que tenía el .env antes).
	if c.MQTT.Puerto == 0 {
		c.MQTT.Puerto = 1883
	}
	if strings.TrimSpace(c.Servidor.Port) == "" {
		c.Servidor.Port = "3000"
	}
	if c.Servidor.CarouselSegundos <= 0 {
		c.Servidor.CarouselSegundos = 10
	}
	// 0 = default (2); fuera de rango se ajusta a 1..3 al crear el store.
	if c.Servidor.SlotsPersonal <= 0 {
		c.Servidor.SlotsPersonal = 2
	}
	if strings.TrimSpace(c.NATS.URL) == "" {
		c.NATS.URL = "nats://localhost:4222"
	}
	// Maquina (nombre visible) cae a MachineCode si no se define, como antes.
	if strings.TrimSpace(c.Servidor.Maquina) == "" {
		c.Servidor.Maquina = c.Servidor.MachineCode
	}
	return &c, nil
}

// validar comprueba lo mínimo para que el dashboard funcione.
func (c *Config) validar() error {
	mc := strings.TrimSpace(c.Servidor.MachineCode)
	if mc == "" || strings.ContainsAny(mc, " \t*>") {
		return fmt.Errorf("servidor.machine_code inválido o vacío (%q). Ej: B2-A-silo-1", c.Servidor.MachineCode)
	}
	if strings.TrimSpace(c.MQTT.Broker) == "" {
		return fmt.Errorf("mqtt.broker es obligatorio")
	}
	if len(c.Valvuladoras) == 0 {
		return fmt.Errorf("define al menos una valvuladora")
	}
	for i, v := range c.Valvuladoras {
		if strings.TrimSpace(v.PesoTopic) == "" {
			return fmt.Errorf("valvuladora %d (%q): falta peso_topic", i+1, v.Codigo)
		}
	}
	return nil
}

// Codigos devuelve los códigos de las valvuladoras, en orden.
func (c *Config) Codigos() []string {
	out := make([]string, len(c.Valvuladoras))
	for i, v := range c.Valvuladoras {
		out[i] = v.Codigo
	}
	return out
}

// NumValvuladoras devuelve cuántas bolas tiene el silo (según el YAML).
func (c *Config) NumValvuladoras() int {
	return len(c.Valvuladoras)
}

// BrokerURL arma la URL del broker para el cliente MQTT (tcp:// o ssl://).
func (c *Config) BrokerURL() string {
	esquema := "tcp"
	if c.MQTT.TLS {
		esquema = "ssl"
	}
	return fmt.Sprintf("%s://%s:%d", esquema, c.MQTT.Broker, c.MQTT.Puerto)
}

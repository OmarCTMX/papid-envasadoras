package config

import (
	"os"
	"path/filepath"
	"testing"
)

func escribirTmp(t *testing.T, contenido string) string {
	t.Helper()
	dir := t.TempDir()
	ruta := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(ruta, []byte(contenido), 0o644); err != nil {
		t.Fatal(err)
	}
	return ruta
}

func TestCargarOK(t *testing.T) {
	ruta := escribirTmp(t, `
servidor:
  machine_code: "B2-A-silo-2"
  region: "Cotija"
  reset_hora: "18:00"
nats:
  url: "nats://192.168.90.156:4222"
  usuario: "papid"
  password: "papid2024"
mqtt:
  broker: "10.0.1.176"
  puerto: 1883
valvuladoras:
  - codigo: "b2-b-v1"
    peso_topic: "plc/s7/b2-b-v1"
  - codigo: "b2-b-v2"
    peso_topic: "plc/s7/b2-b-v2"
`)
	c, err := Cargar(ruta)
	if err != nil {
		t.Fatalf("Cargar falló: %v", err)
	}
	if c.NumValvuladoras() != 2 {
		t.Fatalf("esperaba 2 valvuladoras, obtuve %d", c.NumValvuladoras())
	}
	cods := c.Codigos()
	if cods[0] != "b2-b-v1" || cods[1] != "b2-b-v2" {
		t.Fatalf("códigos mal: %v", cods)
	}
	if c.BrokerURL() != "tcp://10.0.1.176:1883" {
		t.Fatalf("BrokerURL mal: %s", c.BrokerURL())
	}
	if c.Servidor.MachineCode != "B2-A-silo-2" || c.NATS.Password != "papid2024" {
		t.Fatalf("servidor/nats mal: %+v %+v", c.Servidor, c.NATS)
	}
}

// TestDefaults: puerto web 3000, NATS localhost y maquina = machine_code
// cuando no se definen (mismos defaults que tenía el .env).
func TestDefaults(t *testing.T) {
	ruta := escribirTmp(t, `
servidor:
  machine_code: "B2-A-silo-1"
mqtt:
  broker: "host"
valvuladoras:
  - codigo: "v1"
    peso_topic: "t/v1"
`)
	c, err := Cargar(ruta)
	if err != nil {
		t.Fatal(err)
	}
	if c.Servidor.Port != "3000" {
		t.Fatalf("port default debía ser 3000, obtuve %q", c.Servidor.Port)
	}
	if c.NATS.URL != "nats://localhost:4222" {
		t.Fatalf("nats.url default mal: %q", c.NATS.URL)
	}
	if c.Servidor.Maquina != "B2-A-silo-1" {
		t.Fatalf("maquina debía caer a machine_code: %q", c.Servidor.Maquina)
	}
	if c.MQTT.Puerto != 1883 {
		t.Fatalf("puerto MQTT default debía ser 1883, obtuve %d", c.MQTT.Puerto)
	}
}

func TestTLSUsaSSL(t *testing.T) {
	ruta := escribirTmp(t, `
servidor:
  machine_code: "B2-A-silo-1"
mqtt:
  broker: "host"
  puerto: 8883
  tls: true
valvuladoras:
  - codigo: "v1"
    peso_topic: "t/v1"
`)
	c, err := Cargar(ruta)
	if err != nil {
		t.Fatal(err)
	}
	if c.BrokerURL() != "ssl://host:8883" {
		t.Fatalf("con tls debía ser ssl://, obtuve %s", c.BrokerURL())
	}
}

func TestSinMachineCodeFalla(t *testing.T) {
	ruta := escribirTmp(t, `
mqtt:
  broker: "host"
valvuladoras:
  - codigo: "v1"
    peso_topic: "t/v1"
`)
	if _, err := Cargar(ruta); err == nil {
		t.Fatal("debía fallar sin servidor.machine_code")
	}
}

func TestSinValvuladorasFalla(t *testing.T) {
	ruta := escribirTmp(t, `
servidor:
  machine_code: "B2-A-silo-1"
mqtt:
  broker: "host"
valvuladoras: []
`)
	if _, err := Cargar(ruta); err == nil {
		t.Fatal("debía fallar sin valvuladoras")
	}
}

func TestTopicVacioFalla(t *testing.T) {
	ruta := escribirTmp(t, `
servidor:
  machine_code: "B2-A-silo-1"
mqtt:
  broker: "host"
valvuladoras:
  - codigo: "v1"
    peso_topic: ""
`)
	if _, err := Cargar(ruta); err == nil {
		t.Fatal("debía fallar con peso_topic vacío")
	}
}

func TestSinBrokerFalla(t *testing.T) {
	ruta := escribirTmp(t, `
servidor:
  machine_code: "B2-A-silo-1"
valvuladoras:
  - codigo: "v1"
    peso_topic: "t/v1"
`)
	if _, err := Cargar(ruta); err == nil {
		t.Fatal("debía fallar sin broker")
	}
}

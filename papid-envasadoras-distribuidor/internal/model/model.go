// Package model define el contrato de datos del distribuidor.
//
// El distribuidor recibe UN mensaje (el "blob") con el proceso de TODAS las
// envasadoras (una sola conexión al PLC vía Node-RED) y publica el pedazo de
// cada envasadora en su subject papid.envasadora.<code>, que es exactamente lo
// que consume cada dashboard.
package model

// Leds son las tres luces por valvuladora (solo visuales, del PLC).
type Leds struct {
	Paro     bool `json:"paro"`
	Limpieza bool `json:"limpieza"`
	Ciclo    bool `json:"ciclo"`
}

// Registro es un bulto ya envasado (tabla ID / Peso / Fecha del dashboard).
type Registro struct {
	ID    string  `json:"id"`
	Peso  float64 `json:"peso"`
	Fecha string  `json:"fecha"`
}

// Valvuladora es el estado de una de las tres valvuladoras de una envasadora.
type Valvuladora struct {
	Peso      float64    `json:"peso"`
	Setpoint  float64    `json:"setpoint"`
	Columna   float64    `json:"columna"`
	Leds      *Leds      `json:"leds,omitempty"`
	Bultos    int        `json:"bultos"` // contador de bultos del PLC
	Registros []Registro `json:"registros,omitempty"`
	// Datos del último bulto completado (opcionales, del PLC). Se reenvían tal
	// cual; IDBulto es any para aceptar texto o número sin perderlo.
	IDBulto   any     `json:"id_bulto,omitempty"`
	PesoBulto float64 `json:"peso_bulto,omitempty"`
}

// Envasadora es el estado COMPLETO de una envasadora. Es el contrato que el
// dashboard consume en papid.envasadora.<machine_code>. El distribuidor publica
// un objeto de estos por cada envasadora que venga en el blob.
type Envasadora struct {
	MachineCode  string        `json:"machine_code"`
	OF           string        `json:"of"`
	ItemCode     string        `json:"item_code"`
	ItemName     string        `json:"item_name"`
	PesoProducto float64       `json:"peso_producto"`
	LoteActual   int           `json:"lote_actual"`
	LotesTotales int           `json:"lotes_totales"`
	Valvuladoras []Valvuladora `json:"valvuladoras"`
}

// Blob es lo que Node-RED publica con TODAS las envasadoras en un solo mensaje
// (papid.envasadoras.plc). El distribuidor lo parte y publica cada envasadora.
//
// Acepta dos formas, para no atar a Node-RED a una sola:
//   - { "envasadoras": [ {Envasadora}, {Envasadora}, ... ] }
//   - [ {Envasadora}, {Envasadora}, ... ]   (arreglo directo; ver DecodBlob)
type Blob struct {
	Envasadoras []Envasadora `json:"envasadoras"`
}

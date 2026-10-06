// Package model define las estructuras de datos del dashboard de envasadoras.
//
// El dashboard NO publica nada: solo consume el estado de la envasadora desde
// NATS (subject papid.envasadora.<MACHINE_CODE>) y lo muestra. Los valores de
// setpoint y columna se cambian desde Node-RED y llegan ya modificados en el
// mensaje; el frontend solo los refleja.
package model

import (
	"bytes"
	"encoding/json"
	"strings"
)

// TotalValvuladoras es la cantidad fija de valvuladoras por envasadora.
const TotalValvuladoras = 3

// MaxRegistros es el máximo de bultos que se guardan por valvuladora para la
// tabla (ID / Peso / Fecha). La pantalla solo muestra los que caben; el tope
// evita que un productor que mande el historial completo infle cada mensaje SSE.
const MaxRegistros = 20

// FlexString acepta en JSON tanto un texto ("B-0012") como un número (12).
// Se usa para el ID del bulto: así no se pierde el mensaje completo si el PLC o
// Node-RED lo mandan como número en lugar de texto.
type FlexString string

// UnmarshalJSON implementa la conversión flexible (texto, número o null).
func (f *FlexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*f = ""
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = FlexString(s)
		return nil
	}
	// Número (u otro literal): se guarda tal cual viene, sin comillas.
	*f = FlexString(strings.TrimSpace(string(b)))
	return nil
}

// Registro es un bulto ya envasado: se muestra en la tabla de la valvuladora.
type Registro struct {
	ID    FlexString `json:"id"`    // identificador del bulto
	Peso  float64    `json:"peso"`  // peso final del bulto (kg)
	Fecha string     `json:"fecha"` // fecha/hora (ISO 8601 recomendado)
}

// Leds son los estados de las tres luces skeumórficas (solo visuales) que
// llegan del PLC. true = encendida, false = apagada.
type Leds struct {
	Paro     bool `json:"paro"`     // paro de emergencia (rojo)
	Limpieza bool `json:"limpieza"` // limpieza (amarillo)
	Ciclo    bool `json:"ciclo"`    // ciclo de envase (verde)
}

// Valvuladora es el estado de una de las tres valvuladoras de la envasadora.
type Valvuladora struct {
	// Peso actual (kg). Es el número que se muestra dentro de la bola. El
	// llenado de la bola es peso/peso_producto (el tope lo da la orden, no el
	// setpoint), así que el peso NO se acota a un tope fijo aquí.
	Peso float64 `json:"peso"`
	// Setpoint (kg): dato del PLC, editable desde el PLC o Node-RED. SOLO se
	// muestra debajo de la bola; NO afecta el llenado.
	Setpoint float64 `json:"setpoint"`
	// Columna (kg): dato del PLC, editable desde el PLC o Node-RED. Solo se
	// muestra.
	Columna float64 `json:"columna"`
	// Leds: estado de las tres luces del PLC de ESTA valvuladora. Opcional; si
	// no viene, el frontend usa las luces a nivel de la envasadora (Estado.Leds).
	Leds *Leds `json:"leds,omitempty"`
	// Bultos: contador de bultos envasados por ESTA valvuladora en la orden
	// actual. Se muestra debajo de la bola (hasta 4 dígitos, ej. 1000).
	Bultos int `json:"bultos"`
	// Registros: últimos bultos envasados (ID / Peso / Fecha), del más nuevo al
	// más viejo. Se muestran en la tabla a la derecha de los LEDs.
	Registros []Registro `json:"registros"`
}

// Estado es el mensaje completo que llega por NATS y que se refleja en la UI.
//
//	{
//	  "machine_code": "envasadora-1",
//	  "of": "955086",
//	  "item_code": "PESP",
//	  "item_name": "COT. Pulido Espejo Blanco Pegaduro 10 kg",
//	  "peso_producto": 10,
//	  "lote_actual": 1,
//	  "lotes_totales": 10,
//	  "valvuladoras": [
//	    { "peso": 7, "setpoint": 20, "columna": 5,
//	      "leds": { "paro": false, "limpieza": false, "ciclo": true },
//	      "bultos": 124,
//	      "registros": [
//	        { "id": "124", "peso": 10.02, "fecha": "2026-09-30T11:27:32-06:00" }
//	      ] },
//	    ...
//	  ]
//	}
//
// PesoProducto (de la orden, campo pesoProducto) es el 100% de llenado de las
// tres bolas: la bola se llena a peso/peso_producto. El setpoint por
// valvuladora es dato del PLC y solo se muestra; no afecta el llenado.
type Estado struct {
	MachineCode  string        `json:"machine_code"`
	OF           string        `json:"of"`
	ItemCode     string        `json:"item_code"`
	ItemName     string        `json:"item_name"`
	PesoProducto float64       `json:"peso_producto"`
	LoteActual   int           `json:"lote_actual"`
	LotesTotales int           `json:"lotes_totales"`
	Valvuladoras []Valvuladora `json:"valvuladoras"`
	// Leds a nivel de la envasadora (opcional). Se usa cuando las luces son de
	// la máquina completa y no por valvuladora; el frontend las muestra
	// repetidas debajo de cada bola. Si las valvuladoras traen su propio Leds,
	// ese tiene prioridad.
	Leds *Leds `json:"leds,omitempty"`
	// Trabajadores son los nombres del personal asignado a esta envasadora
	// (del emitter, subject papid.emitter.<code>). Se muestran en el footer.
	// No vienen del PLC: los llena el dashboard al escuchar al emitter.
	Trabajadores []string `json:"trabajadores,omitempty"`
}

// EstadoVacio devuelve un estado inicial sin datos: tres valvuladoras en cero.
// Es lo que ve el navegador antes de que llegue el primer mensaje de NATS, para
// que la UI no arranque en blanco ni con menos de tres bolas.
func EstadoVacio() Estado {
	e := Estado{Valvuladoras: make([]Valvuladora, TotalValvuladoras)}
	e.Normalizar() // registros como arreglo vacío, no null
	return e
}

// Normalizar garantiza que el estado siempre tenga exactamente
// TotalValvuladoras elementos y que no haya pesos negativos. El peso NO se
// acota por arriba: el 100% de la bola es el setpoint, así que un peso mayor
// al setpoint simplemente llena la bola al máximo (lo maneja el frontend).
// Un mensaje mal formado (más o menos valvuladoras) no debe romper la UI.
func (e *Estado) Normalizar() {
	if e.Valvuladoras == nil {
		e.Valvuladoras = make([]Valvuladora, 0, TotalValvuladoras)
	}
	// Recorta si vienen de más.
	if len(e.Valvuladoras) > TotalValvuladoras {
		e.Valvuladoras = e.Valvuladoras[:TotalValvuladoras]
	}
	// Rellena si vienen de menos.
	for len(e.Valvuladoras) < TotalValvuladoras {
		e.Valvuladoras = append(e.Valvuladoras, Valvuladora{})
	}
	for i := range e.Valvuladoras {
		v := &e.Valvuladoras[i]
		// Solo evita valores negativos; el tope de la bola lo pone el frontend.
		if v.Peso < 0 {
			v.Peso = 0
		}
		if v.Bultos < 0 {
			v.Bultos = 0
		}
		// Tabla: nunca nil (el frontend espera un arreglo) y como máximo
		// MaxRegistros (se conservan los primeros = los más nuevos).
		if v.Registros == nil {
			v.Registros = []Registro{}
		}
		if len(v.Registros) > MaxRegistros {
			v.Registros = v.Registros[:MaxRegistros]
		}
	}
}

// Package model define las estructuras de datos del dashboard de envasadoras.
//
// El dashboard NO publica nada: solo consume el estado de la envasadora desde
// NATS (subject papid.envasadora.<MACHINE_CODE>) y lo muestra. Los valores de
// setpoint y columna se cambian desde Node-RED y llegan ya modificados en el
// mensaje; el frontend solo los refleja.
package model

// TotalValvuladoras es la cantidad fija de valvuladoras por envasadora.
const TotalValvuladoras = 3

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
//	    { "peso": 7, "setpoint": 20, "columna": 5 },
//	    { "peso": 5, "setpoint": 20, "columna": 5 },
//	    { "peso": 9, "setpoint": 20, "columna": 5 }
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
}

// EstadoVacio devuelve un estado inicial sin datos: tres valvuladoras en cero.
// Es lo que ve el navegador antes de que llegue el primer mensaje de NATS, para
// que la UI no arranque en blanco ni con menos de tres bolas.
func EstadoVacio() Estado {
	return Estado{
		Valvuladoras: make([]Valvuladora, TotalValvuladoras),
	}
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
	// Solo evita pesos negativos; el tope lo define el setpoint en el frontend.
	for i := range e.Valvuladoras {
		if e.Valvuladoras[i].Peso < 0 {
			e.Valvuladoras[i].Peso = 0
		}
	}
}

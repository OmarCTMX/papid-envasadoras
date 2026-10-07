// Package model define las estructuras de datos del dashboard de envasadoras.
//
// El estado de la pantalla se arma con DOS fuentes:
//
//   - La ORDEN (POST /api/orden, la manda el admin): número de orden, nombre,
//     lotes, bultos por lote (invisible), peso del producto y el PERSONAL.
//   - El PROCESO (NATS papid.envasadora.<MACHINE_CODE>, del PLC vía Node-RED /
//     distribuidor): peso de cada bola, contador de bultos del PLC, setpoint,
//     columna y LEDs.
//
// Con esas dos fuentes el dashboard CALCULA: los bultos de la orden (suma de
// las envasadoras desde que llegó la orden), los lotes completados y la tabla
// de bultos (ID / Peso / Fecha).
package model

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// DefaultEnvasadoras es la cantidad de envasadoras (bolas) por defecto si no se
// configura. Cada dashboard lo sobreescribe con NUM_ENVASADORAS del .env.
const DefaultEnvasadoras = 3

// MaxRegistros es el máximo de bultos que se guardan por envasadora para la
// tabla (ID / Peso / Fecha). La pantalla solo muestra los que caben.
const MaxRegistros = 20

// FlexString acepta en JSON tanto un texto ("B-0012") como un número (12).
// Así no se rechaza un mensaje completo solo porque un ID llegó como número.
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
	*f = FlexString(strings.TrimSpace(string(b)))
	return nil
}

// FlexNum acepta un número (20) o un número en texto ("20"). El admin y el PLC
// no siempre mandan los tipos igual; esto evita rechazar la orden por eso.
type FlexNum float64

// UnmarshalJSON implementa la conversión flexible (número, texto o null).
func (f *FlexNum) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*f = 0
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*f = 0
			return nil
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		*f = FlexNum(v)
		return nil
	}
	var v float64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*f = FlexNum(v)
	return nil
}

// Registro es un bulto ya envasado: una fila de la tabla de la envasadora.
// Lo escribe el dashboard en el momento en que el PLC reporta un bulto más.
type Registro struct {
	ID    FlexString `json:"id"`    // ID del bulto (del PLC)
	Peso  float64    `json:"peso"`  // peso del bulto al completarse (kg)
	Fecha string     `json:"fecha"` // hora en que se completó (RFC 3339)
}

// Leds son las tres luces skeumórficas (solo visuales) de una envasadora.
type Leds struct {
	Paro     bool `json:"paro"`     // paro de emergencia (rojo)
	Limpieza bool `json:"limpieza"` // limpieza (amarillo)
	Ciclo    bool `json:"ciclo"`    // ciclo de envase (verde)
}

// Valvuladora es una envasadora del silo (una bola en la pantalla).
//
// Entrada (del PLC): peso, setpoint, columna, leds, bultos (contador del PLC) y,
// opcionalmente, id_bulto / peso_bulto del último bulto completado.
// Salida (a la pantalla): los mismos campos, pero Bultos es el de la ORDEN
// actual (calculado por el dashboard) y Registros es la tabla del dashboard.
type Valvuladora struct {
	Peso     float64 `json:"peso"`     // peso actual en la báscula (kg)
	Setpoint float64 `json:"setpoint"` // kg, del PLC (solo se muestra)
	Columna  float64 `json:"columna"`  // kg, del PLC (solo se muestra)
	Leds     *Leds   `json:"leds,omitempty"`
	// Bultos: al ENTRAR es el contador del PLC; al SALIR es el de la orden
	// actual (se reinicia con cada orden nueva).
	Bultos int `json:"bultos"`
	// IDBulto / PesoBulto: datos del último bulto completado, si el PLC los
	// manda. Se usan para la fila de la tabla. Si no vienen, el ID es el número
	// de bulto de la orden y el peso es el máximo que marcó la báscula.
	IDBulto   FlexString `json:"id_bulto,omitempty"`
	PesoBulto float64    `json:"peso_bulto,omitempty"`
	// Registros: tabla de últimos bultos (del más nuevo al más viejo).
	Registros []Registro `json:"registros"`
}

// Trabajador es una persona asignada al silo (viene en el POST de la orden).
type Trabajador struct {
	Nombre     string `json:"nombre"`
	Rol        string `json:"rol"`
	EmployeeID string `json:"employee_id,omitempty"`
}

// Orden es lo que guarda el dashboard del POST /api/orden. Se ignoran los
// campos que no usa la pantalla (por ejemplo "materiales").
type Orden struct {
	DocEntry            FlexString `json:"doc_entry"`
	DocNum              FlexString `json:"doc_num"`
	Fecha               string     `json:"fecha"`
	ItemCode            string     `json:"item_code"`
	ItemName            string     `json:"item_name"`
	PesoProducto        float64    `json:"peso_producto"`
	NoLotes             int        `json:"no_lotes"`
	CantidadBts         int        `json:"cantidad_bts"` // bultos por lote (invisible)
	FactorProductividad float64    `json:"factor_productividad"`
	Maquina             string     `json:"maquina"`
	Estatus             string     `json:"estatus"`
	Recibida            string     `json:"recibida"` // cuándo llegó al dashboard
}

// Estado es lo que se empuja por SSE a la pantalla (y lo que devuelve
// GET /api/estado). Los campos del header se dejan "planos" para el frontend.
type Estado struct {
	MachineCode  string  `json:"machine_code"`
	OF           string  `json:"of"`            // = orden.doc_num
	ItemCode     string  `json:"item_code"`     // = orden.item_code
	ItemName     string  `json:"item_name"`     // = orden.item_name
	PesoProducto float64 `json:"peso_producto"` // 100% de las bolas
	// LoteActual son los lotes COMPLETADOS: ⌊bultos_orden / bultos_por_lote⌋,
	// con tope en LotesTotales. Al llegar a LotesTotales la orden terminó.
	LoteActual     int  `json:"lote_actual"`
	LotesTotales   int  `json:"lotes_totales"`
	OrdenTerminada bool `json:"orden_terminada"`
	// No se muestran en pantalla (el operador solo ve los bultos de cada bola).
	BultosOrden   int `json:"bultos_orden"`
	BultosPorLote int `json:"bultos_por_lote"`

	Valvuladoras []Valvuladora `json:"valvuladoras"`
	// Leds a nivel de la máquina (opcional, compatibilidad). Si cada
	// envasadora trae sus propios leds, esos tienen prioridad.
	Leds *Leds `json:"leds,omitempty"`
	// Trabajadores: personal del silo (viene en el POST de la orden).
	Trabajadores []Trabajador `json:"trabajadores"`
}

// Snapshot es lo que se guarda en NATS KV para sobrevivir a un reinicio: la
// orden, el personal, los contadores y la tabla. El peso y los LEDs no se
// guardan porque el PLC los vuelve a mandar en segundos.
type Snapshot struct {
	Orden        *Orden       `json:"orden,omitempty"`
	Trabajadores []Trabajador `json:"trabajadores"`
	// Por envasadora: bultos de la orden, último contador visto del PLC (-1 =
	// todavía no se ve) y tabla de registros.
	BultosOrden []int        `json:"bultos_orden"`
	UltimoPLC   []int        `json:"ultimo_plc"`
	Registros   [][]Registro `json:"registros"`
	Guardado    string       `json:"guardado"`
}

// EstadoVacio devuelve un estado inicial sin datos: N envasadoras en cero.
func EstadoVacio(numEnvasadoras int) Estado {
	if numEnvasadoras <= 0 {
		numEnvasadoras = DefaultEnvasadoras
	}
	e := Estado{Valvuladoras: make([]Valvuladora, numEnvasadoras)}
	e.Normalizar(numEnvasadoras)
	return e
}

// Normalizar garantiza que el estado tenga exactamente numEnvasadoras
// envasadoras, sin valores negativos y con arreglos (no null) para el frontend.
// Un mensaje mal formado (más o menos envasadoras) no debe romper la UI.
func (e *Estado) Normalizar(numEnvasadoras int) {
	if numEnvasadoras <= 0 {
		numEnvasadoras = DefaultEnvasadoras
	}
	if e.Valvuladoras == nil {
		e.Valvuladoras = make([]Valvuladora, 0, numEnvasadoras)
	}
	if len(e.Valvuladoras) > numEnvasadoras {
		e.Valvuladoras = e.Valvuladoras[:numEnvasadoras]
	}
	for len(e.Valvuladoras) < numEnvasadoras {
		e.Valvuladoras = append(e.Valvuladoras, Valvuladora{})
	}
	for i := range e.Valvuladoras {
		v := &e.Valvuladoras[i]
		if v.Peso < 0 {
			v.Peso = 0
		}
		if v.Bultos < 0 {
			v.Bultos = 0
		}
		if v.Registros == nil {
			v.Registros = []Registro{}
		}
		if len(v.Registros) > MaxRegistros {
			v.Registros = v.Registros[:MaxRegistros]
		}
	}
	if e.Trabajadores == nil {
		e.Trabajadores = []Trabajador{}
	}
}

// LotesCompletados calcula los lotes terminados: ⌊bultos / porLote⌋, sin pasar
// de total. Ejemplo con porLote=30: 30 → 1, 60 → 2, 70 → 2.
// Si porLote o total no están definidos, devuelve 0.
func LotesCompletados(bultos, porLote, total int) int {
	if porLote <= 0 || total <= 0 || bultos <= 0 {
		return 0
	}
	n := bultos / porLote
	if n > total {
		n = total
	}
	return n
}

// CodigoDeMaquina traduce el nombre de máquina del admin ("B2 (A) 3") al
// machine_code del dashboard ("B2-A-silo-3"). Si ya viene en formato de
// machine_code ("B2-A-silo-3") lo deja igual. Devuelve "" si no lo entiende.
func CodigoDeMaquina(maquina string) string {
	m := strings.TrimSpace(maquina)
	if m == "" {
		return ""
	}
	// Formato del admin: "<linea> (<sub>) <silo>", ej. "B2 (A) 3".
	if i := strings.Index(m, "("); i > 0 {
		j := strings.Index(m, ")")
		if j > i {
			linea := strings.TrimSpace(m[:i])
			sub := strings.TrimSpace(m[i+1 : j])
			silo := strings.TrimSpace(m[j+1:])
			if linea != "" && sub != "" && silo != "" && !strings.ContainsAny(silo, " ()") {
				return linea + "-" + sub + "-silo-" + silo
			}
		}
		return ""
	}
	// Ya viene como machine_code.
	if !strings.ContainsAny(m, " ()") {
		return m
	}
	return ""
}

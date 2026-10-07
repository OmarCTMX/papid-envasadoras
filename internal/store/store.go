// Package store mantiene en memoria el estado del silo y hace los cálculos de
// la orden.
//
// Recibe dos cosas:
//   - AplicarOrden: la orden + personal del POST /api/orden. Reinicia los
//     bultos y la tabla (una orden nueva empieza en cero).
//   - AplicarProceso: los datos del PLC (peso, setpoint, columna, leds y el
//     CONTADOR de bultos del PLC de cada envasadora).
//
// Con eso calcula:
//   - Bultos de la orden por envasadora: se suman los INCREMENTOS del contador
//     del PLC desde que llegó la orden. Así no importa si el PLC reinicia o no
//     su contador entre órdenes.
//   - Lotes completados: ⌊suma de bultos / bultos_por_lote⌋, con tope en
//     no_lotes.
//   - Tabla de bultos: por cada bulto nuevo, una fila con ID, peso y hora.
package store

import (
	"strconv"
	"sync"
	"time"

	"papid-envasadoras/internal/model"
)

// maxFilasPorMensaje acota cuántas filas se agregan a la tabla por un solo
// mensaje del PLC (si el contador brinca mucho, por ejemplo tras una
// desconexión, no se llena la tabla de filas sin datos reales).
const maxFilasPorMensaje = 5

// Store guarda el estado del silo y avisa cuando cambia.
type Store struct {
	mu          sync.RWMutex
	machineCode string
	n           int // número de envasadoras (bolas) del silo

	orden        *model.Orden
	trabajadores []model.Trabajador

	// Por envasadora:
	bultosOrden []int              // bultos de la orden actual
	ultimoPLC   []int              // último contador del PLC visto (-1 = aún no)
	pesoMax     []float64          // peso máximo del ciclo en curso (para la tabla)
	registros   [][]model.Registro // tabla (más nuevo primero)

	proceso model.Estado // último proceso del PLC (peso, setpoint, columna, leds)

	onChange  func()               // empuja el estado por SSE
	onGuardar func(model.Snapshot) // persiste en KV (orden, contadores, tabla)
}

// New crea un store vacío para un silo con n envasadoras.
func New(machineCode string, n int) *Store {
	if n <= 0 {
		n = model.DefaultEnvasadoras
	}
	s := &Store{machineCode: machineCode, n: n}
	s.limpiarContadores()
	for i := range s.ultimoPLC {
		s.ultimoPLC[i] = -1
	}
	s.proceso = model.EstadoVacio(n)
	return s
}

// limpiarContadores deja bultos y tabla en cero (conserva ultimoPLC si ya
// existe, para seguir contando desde el valor actual del PLC). Requiere lock.
func (s *Store) limpiarContadores() {
	s.bultosOrden = make([]int, s.n)
	s.pesoMax = make([]float64, s.n)
	s.registros = make([][]model.Registro, s.n)
	for i := range s.registros {
		s.registros[i] = []model.Registro{}
	}
	if len(s.ultimoPLC) != s.n {
		s.ultimoPLC = make([]int, s.n)
		for i := range s.ultimoPLC {
			s.ultimoPLC[i] = -1
		}
	}
}

// SetOnChange registra el callback que se ejecuta cada vez que el estado cambia.
func (s *Store) SetOnChange(fn func()) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// SetOnGuardar registra el callback que persiste el snapshot (KV). Solo se
// llama cuando cambia algo que vale la pena guardar (orden, bultos, tabla),
// no con cada lectura de peso.
func (s *Store) SetOnGuardar(fn func(model.Snapshot)) {
	s.mu.Lock()
	s.onGuardar = fn
	s.mu.Unlock()
}

// MachineCode devuelve el código de este silo.
func (s *Store) MachineCode() string { return s.machineCode }

// NumEnvasadoras devuelve cuántas envasadoras tiene este silo.
func (s *Store) NumEnvasadoras() int { return s.n }

// TieneOrden indica si hay una orden cargada.
func (s *Store) TieneOrden() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.orden != nil
}

// Orden devuelve una copia de la orden actual (nil si no hay).
func (s *Store) Orden() (*model.Orden, []model.Trabajador) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.orden == nil {
		return nil, append([]model.Trabajador{}, s.trabajadores...)
	}
	o := *s.orden
	return &o, append([]model.Trabajador{}, s.trabajadores...)
}

// AplicarOrden carga una orden nueva (del POST). Reinicia los bultos de la
// orden y la tabla: cada orden empieza en cero.
func (s *Store) AplicarOrden(o model.Orden, trabajadores []model.Trabajador) {
	s.mu.Lock()
	copia := o
	s.orden = &copia
	if trabajadores == nil {
		trabajadores = []model.Trabajador{}
	}
	s.trabajadores = trabajadores
	s.limpiarContadores()
	snap := s.snapshotLocked()
	s.mu.Unlock()

	s.guardar(snap)
	s.notificar()
}

// QuitarOrden borra la orden, el personal y los contadores (fin de orden,
// reseteo diario o DELETE /api/orden).
func (s *Store) QuitarOrden() {
	s.mu.Lock()
	s.orden = nil
	s.trabajadores = []model.Trabajador{}
	s.limpiarContadores()
	snap := s.snapshotLocked()
	s.mu.Unlock()

	s.guardar(snap)
	s.notificar()
}

// AplicarProceso recibe los datos del PLC. Actualiza peso/setpoint/columna/leds
// y, si hay orden, convierte los incrementos del contador del PLC en bultos de
// la orden y en filas de la tabla.
func (s *Store) AplicarProceso(e model.Estado) {
	e.Normalizar(s.n)
	ahora := time.Now()

	s.mu.Lock()
	huboBulto := false
	for i := 0; i < s.n; i++ {
		v := e.Valvuladoras[i]
		cur := v.Bultos

		// Cuántos bultos nuevos hubo desde el último mensaje.
		delta := 0
		switch {
		case s.ultimoPLC[i] < 0:
			// Primera lectura: es la base, no son bultos nuevos.
		case cur >= s.ultimoPLC[i]:
			delta = cur - s.ultimoPLC[i]
		default:
			// El contador del PLC bajó: se reinició. Lo que marca ahora son
			// bultos hechos desde el reinicio.
			delta = cur
		}
		s.ultimoPLC[i] = cur

		if delta > 0 && s.orden != nil {
			huboBulto = true
			s.bultosOrden[i] += delta

			// Peso del bulto: el que mande el PLC, o el máximo que marcó la
			// báscula durante el ciclo (incluida esta lectura).
			peso := v.PesoBulto
			if peso <= 0 {
				peso = s.pesoMax[i]
				if v.Peso > peso {
					peso = v.Peso
				}
			}
			filas := delta
			if filas > maxFilasPorMensaje {
				filas = maxFilasPorMensaje
			}
			nuevas := make([]model.Registro, 0, filas)
			for k := 0; k < filas; k++ {
				// La primera fila es la del bulto más reciente.
				num := s.bultosOrden[i] - k
				id := v.IDBulto
				if id == "" || k > 0 {
					id = model.FlexString(strconv.Itoa(num))
				}
				nuevas = append(nuevas, model.Registro{
					ID: id, Peso: redondear2(peso), Fecha: ahora.Format(time.RFC3339),
				})
			}
			// Se arma un slice NUEVO (no se modifica el anterior, que puede
			// estar en un snapshot que se está serializando).
			tabla := append(nuevas, s.registros[i]...)
			if len(tabla) > model.MaxRegistros {
				tabla = tabla[:model.MaxRegistros]
			}
			s.registros[i] = tabla
			s.pesoMax[i] = 0 // empieza el ciclo del siguiente bulto
		} else if v.Peso > s.pesoMax[i] {
			s.pesoMax[i] = v.Peso
		}
	}
	s.proceso = e
	var snap model.Snapshot
	if huboBulto {
		snap = s.snapshotLocked()
	}
	s.mu.Unlock()

	if huboBulto {
		s.guardar(snap)
	}
	s.notificar()
}

// Restaurar carga un snapshot del KV (al arrancar). Si ya llegó una orden por
// POST antes de conectar a NATS, NO se pisa con lo guardado.
func (s *Store) Restaurar(snap model.Snapshot) bool {
	s.mu.Lock()
	if s.orden != nil {
		s.mu.Unlock()
		return false
	}
	if snap.Orden != nil {
		o := *snap.Orden
		s.orden = &o
	}
	s.trabajadores = snap.Trabajadores
	if s.trabajadores == nil {
		s.trabajadores = []model.Trabajador{}
	}
	s.limpiarContadores()
	for i := 0; i < s.n; i++ {
		if i < len(snap.BultosOrden) {
			s.bultosOrden[i] = snap.BultosOrden[i]
		}
		if i < len(snap.UltimoPLC) {
			s.ultimoPLC[i] = snap.UltimoPLC[i]
		}
		if i < len(snap.Registros) && snap.Registros[i] != nil {
			s.registros[i] = snap.Registros[i]
		}
	}
	s.mu.Unlock()
	s.notificar()
	return true
}

// Estado arma el estado completo para la pantalla (copia segura).
func (s *Store) Estado() model.Estado {
	s.mu.RLock()
	defer s.mu.RUnlock()

	e := model.Estado{
		MachineCode:  s.machineCode,
		Leds:         s.proceso.Leds,
		Valvuladoras: make([]model.Valvuladora, s.n),
		Trabajadores: append([]model.Trabajador{}, s.trabajadores...),
	}
	total := 0
	for i := 0; i < s.n; i++ {
		v := s.proceso.Valvuladoras[i]
		v.Bultos = s.bultosOrden[i] // lo que ve el operador: bultos de la orden
		v.IDBulto, v.PesoBulto = "", 0
		v.Registros = s.registros[i]
		e.Valvuladoras[i] = v
		total += s.bultosOrden[i]
	}
	e.BultosOrden = total

	if o := s.orden; o != nil {
		e.OF = string(o.DocNum)
		e.ItemCode = o.ItemCode
		e.ItemName = o.ItemName
		e.PesoProducto = o.PesoProducto
		e.LotesTotales = o.NoLotes
		e.BultosPorLote = o.CantidadBts
		e.LoteActual = model.LotesCompletados(total, o.CantidadBts, o.NoLotes)
		e.OrdenTerminada = o.NoLotes > 0 && e.LoteActual >= o.NoLotes
	}
	// Sin orden (o sin peso del producto), la bola usa el del PLC si lo manda.
	if e.PesoProducto <= 0 {
		e.PesoProducto = s.proceso.PesoProducto
	}
	return e
}

// Snapshot devuelve lo que se guardaría en KV en este momento.
func (s *Store) Snapshot() model.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}

// snapshotLocked arma lo que se guarda en KV. Requiere el lock tomado.
func (s *Store) snapshotLocked() model.Snapshot {
	snap := model.Snapshot{
		Trabajadores: append([]model.Trabajador{}, s.trabajadores...),
		BultosOrden:  append([]int{}, s.bultosOrden...),
		UltimoPLC:    append([]int{}, s.ultimoPLC...),
		Registros:    make([][]model.Registro, s.n),
		Guardado:     time.Now().Format(time.RFC3339),
	}
	if s.orden != nil {
		o := *s.orden
		snap.Orden = &o
	}
	copy(snap.Registros, s.registros)
	return snap
}

func (s *Store) guardar(snap model.Snapshot) {
	s.mu.RLock()
	fn := s.onGuardar
	s.mu.RUnlock()
	if fn != nil {
		fn(snap)
	}
}

func (s *Store) notificar() {
	s.mu.RLock()
	fn := s.onChange
	s.mu.RUnlock()
	if fn != nil {
		fn()
	}
}

func redondear2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

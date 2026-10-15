// Package personal mantiene el estado EN VIVO del personal del silo (login /
// logout con tarjeta), tal como lo publica el emitter por NATS. Es lo que se
// pinta en la pantalla de personal del carrusel, con el mismo diseño y
// transiciones del Dashboard Personal (dashboardGo).
//
// Fuente: papid.emitter.<MACHINE_CODE> (y el KV papid_personal_asignado al
// arrancar/reconectar). El emitter ya resolvió login/logout; aquí solo se
// traduce su "status" a los 3 estados visuales:
//
//	Activo             -> active  (verde,   "En linea")
//	Asignado/Inactivo  -> pending (naranja, "Sin registrar")
//	sin persona        -> empty   (gris,    "Vacante")
//
// No confundir con la lista de personal del POST /api/personal (store), que
// es solo informativa para el footer.
package personal

import (
	"strings"
	"sync"
)

// Estados visuales de un slot (mismos nombres que dashboardGo).
const (
	EstadoEmpty   = "empty"
	EstadoPending = "pending"
	EstadoActive  = "active"
)

// Límites de slots (igual que dashboardGo: 1 a 3).
const (
	MinSlots     = 1
	MaxSlots     = 3
	DefaultSlots = 2
)

// ClampSlots ajusta n a [1, 3]; 0 o negativo = default.
func ClampSlots(n int) int {
	if n < MinSlots {
		return DefaultSlots
	}
	if n > MaxSlots {
		return MaxSlots
	}
	return n
}

// Slot es lo que la pantalla necesita de cada posición.
type Slot struct {
	Nombre string `json:"nombre"`
	Rol    string `json:"rol"`
	Estado string `json:"estado"` // empty | pending | active
}

// Vacante es un slot sin persona.
func Vacante() Slot { return Slot{Nombre: "No asignado", Rol: "—", Estado: EstadoEmpty} }

// PersonaSync es una persona tal como la publica el emitter. Los demás campos
// del mensaje (employee_id, usb_port, ...) se ignoran.
type PersonaSync struct {
	Nombre string `json:"nombre"`
	Rol    string `json:"rol"`
	Status string `json:"status"` // Asignado | Activo | Inactivo
}

// MensajeSync es el mensaje del emitter en papid.emitter.<code>.
type MensajeSync struct {
	MachineCode string        `json:"machine_code"`
	Personnel   []PersonaSync `json:"personnel"`
}

// Store guarda los slots y avisa cuando cambian.
type Store struct {
	mu       sync.RWMutex
	slots    []Slot
	onChange func()
}

// New crea un store con n slots vacantes (n se ajusta a 1..3).
func New(n int) *Store {
	n = ClampSlots(n)
	s := &Store{slots: make([]Slot, n)}
	for i := range s.slots {
		s.slots[i] = Vacante()
	}
	return s
}

// SetOnChange registra el callback de cambio (empuja por SSE).
func (s *Store) SetOnChange(fn func()) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// NumSlots devuelve cuántos slots muestra la pantalla.
func (s *Store) NumSlots() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.slots)
}

// Slots devuelve una copia de los slots.
func (s *Store) Slots() []Slot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Slot{}, s.slots...)
}

// estadoDe traduce el status del emitter al estado visual.
func estadoDe(status string) string {
	if strings.EqualFold(strings.TrimSpace(status), "Activo") {
		return EstadoActive
	}
	return EstadoPending // Asignado, Inactivo o desconocido: asignado sin registrar
}

// AplicarSync reemplaza TODOS los slots por posición con el personal del
// emitter (las personas que sobran se ignoran; los slots que faltan quedan
// vacantes). Solo notifica si algo cambió.
func (s *Store) AplicarSync(personas []PersonaSync) {
	s.mu.Lock()
	cambio := false
	for i := range s.slots {
		nuevo := Vacante()
		if i < len(personas) {
			p := personas[i]
			nuevo = Slot{
				Nombre: valorODefault(strings.TrimSpace(p.Nombre), "Sin nombre"),
				Rol:    valorODefault(strings.TrimSpace(p.Rol), "—"),
				Estado: estadoDe(p.Status),
			}
		}
		if s.slots[i] != nuevo {
			s.slots[i] = nuevo
			cambio = true
		}
	}
	fn := s.onChange
	s.mu.Unlock()
	if cambio && fn != nil {
		fn()
	}
}

func valorODefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

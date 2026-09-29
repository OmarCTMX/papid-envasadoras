// Package store mantiene en memoria el estado actual de la envasadora.
//
// Es la única fuente de verdad que consulta el resto del sistema (SSE, API).
// Se actualiza cuando llega un mensaje de NATS y notifica a quien escuche
// (el broker SSE) para empujar el nuevo estado a los navegadores.
package store

import (
	"sync"

	"papid-envasadoras/internal/model"
)

// Store guarda el estado y avisa cuando cambia.
type Store struct {
	mu          sync.RWMutex
	machineCode string
	estado      model.Estado
	onChange    func()
}

// New crea un store con el estado inicial vacío (tres valvuladoras en cero).
func New(machineCode string) *Store {
	return &Store{
		machineCode: machineCode,
		estado:      model.EstadoVacio(),
	}
}

// SetOnChange registra el callback que se ejecuta cada vez que el estado cambia.
func (s *Store) SetOnChange(fn func()) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// MachineCode devuelve el código de esta envasadora.
func (s *Store) MachineCode() string {
	return s.machineCode
}

// Aplicar reemplaza el estado con el que llegó por NATS y notifica el cambio.
// El estado se normaliza para que siempre tenga tres valvuladoras válidas.
func (s *Store) Aplicar(estado model.Estado) {
	estado.Normalizar()

	s.mu.Lock()
	s.estado = estado
	onChange := s.onChange
	s.mu.Unlock()

	if onChange != nil {
		onChange()
	}
}

// Estado devuelve una copia del estado actual (segura para leer sin candados).
func (s *Store) Estado() model.Estado {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Copia profunda del slice para que el llamador no comparta el arreglo
	// interno (evita carreras si lo modifica).
	e := s.estado
	e.Valvuladoras = append([]model.Valvuladora(nil), s.estado.Valvuladoras...)
	return e
}

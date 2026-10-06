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
	mu             sync.RWMutex
	machineCode    string
	numEnvasadoras int
	estado         model.Estado
	onChange       func()
}

// New crea un store con el estado inicial vacío (N envasadoras en cero).
func New(machineCode string, numEnvasadoras int) *Store {
	return &Store{
		machineCode:    machineCode,
		numEnvasadoras: numEnvasadoras,
		estado:         model.EstadoVacio(numEnvasadoras),
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

// Aplicar actualiza SOLO los datos de PROCESO (los que manda el distribuidor:
// valvuladoras, peso_producto). CONSERVA los datos del signed (OF, item, lote y
// personal), que llegan por el emitter y mandan sobre el header/footer. Así el
// blob del PLC no borra la orden. El estado se normaliza para tener siempre
// tres valvuladoras.
func (s *Store) Aplicar(estado model.Estado) {
	estado.Normalizar(s.numEnvasadoras)

	s.mu.Lock()
	// La orden y el personal son del signed: se conservan, no los pisa el PLC.
	estado.OF = s.estado.OF
	estado.ItemCode = s.estado.ItemCode
	estado.ItemName = s.estado.ItemName
	estado.LoteActual = s.estado.LoteActual
	estado.LotesTotales = s.estado.LotesTotales
	estado.Trabajadores = s.estado.Trabajadores
	s.estado = estado
	onChange := s.onChange
	s.mu.Unlock()

	if onChange != nil {
		onChange()
	}
}

// DatosOrden son los campos que vienen del signed (vía emitter): el header del
// dashboard (OF/item/lote) y el personal. NO incluye nada del proceso (bolas).
type DatosOrden struct {
	OF           string
	ItemCode     string
	ItemName     string
	LoteActual   int
	LotesTotales int
	Trabajadores []string
}

// AplicarOrden actualiza SOLO los datos del signed (orden + personal) sin tocar
// los datos de proceso (peso, setpoint, columna, leds, bultos). Se llama al
// escuchar papid.emitter.<code>. Así la orden/personal mandan desde el signed
// y las bolas desde el distribuidor, sin pisarse.
func (s *Store) AplicarOrden(d DatosOrden) {
	s.mu.Lock()
	s.estado.OF = d.OF
	s.estado.ItemCode = d.ItemCode
	s.estado.ItemName = d.ItemName
	s.estado.LoteActual = d.LoteActual
	s.estado.LotesTotales = d.LotesTotales
	s.estado.Trabajadores = d.Trabajadores
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

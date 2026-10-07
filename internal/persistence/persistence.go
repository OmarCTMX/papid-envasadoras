// Package persistence guarda en NATS JetStream KV el estado de la orden de
// cada silo, para que un reinicio o un apagón no pierda la orden, el personal,
// los bultos contados ni la tabla.
//
//	Bucket: "papid_envasadoras_orden"
//	Clave:  "<MACHINE_CODE>"   (ej. "B2-A-silo-1")
//	Valor:  JSON de model.Snapshot
//
// Cada dashboard escribe SOLO su propia clave. Si el KV no está disponible el
// dashboard sigue funcionando, solo sin persistencia.
package persistence

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go"

	"papid-envasadoras/internal/model"
)

const bucketName = "papid_envasadoras_orden"

// KV envuelve el acceso al bucket.
type KV struct {
	kv nats.KeyValue
}

// New abre el bucket o lo crea si no existe.
func New(nc *nats.Conn) (*KV, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("jetstream: %w", err)
	}
	kv, err := js.KeyValue(bucketName)
	if errors.Is(err, nats.ErrBucketNotFound) {
		kv, err = js.CreateKeyValue(&nats.KeyValueConfig{
			Bucket:      bucketName,
			Description: "Orden, personal y contadores por silo (dashboard de envasadoras)",
			History:     1,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("bucket %q: %w", bucketName, err)
	}
	return &KV{kv: kv}, nil
}

// Guardar persiste el snapshot del silo.
func (k *KV) Guardar(code string, snap model.Snapshot) error {
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	_, err = k.kv.Put(code, data)
	return err
}

// --- Lectura del KV del EMITTER (solo lectura) ---
//
// El emitter guarda su orden/personal (la del signed) en otro bucket. El
// dashboard lo lee para la vista /control, donde se comparan la orden del
// emitter (NATS) y la de la API (POST). Si el bucket no existe (el emitter no
// ha corrido), simplemente no hay orden del emitter.

const bucketEmitter = "papid_personal_asignado"

// OrdenEmitter es lo mínimo que el dashboard muestra de la orden del emitter.
type OrdenEmitter struct {
	MachineCode  string           `json:"machine_code"`
	MachineName  string           `json:"machine_name"`
	IsActive     bool             `json:"is_active"`
	OrderDetails map[string]any   `json:"order_details"`
	Personal     []map[string]any `json:"personal"`
	Actualizado  string           `json:"actualizado"`
}

// EmitterKV lee (sin escribir) el bucket del emitter.
type EmitterKV struct {
	kv nats.KeyValue
}

// NewEmitter abre el bucket del emitter en modo lectura. Devuelve (nil, nil) si
// el bucket aún no existe: el dashboard funciona igual, solo sin esa columna.
func NewEmitter(nc *nats.Conn) (*EmitterKV, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("jetstream: %w", err)
	}
	kv, err := js.KeyValue(bucketEmitter)
	if errors.Is(err, nats.ErrBucketNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("bucket %q: %w", bucketEmitter, err)
	}
	return &EmitterKV{kv: kv}, nil
}

// Cargar lee la orden/personal del emitter para un silo. nil = sin dato.
func (e *EmitterKV) Cargar(code string) (*OrdenEmitter, error) {
	if e == nil {
		return nil, nil
	}
	entry, err := e.kv.Get(code)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var o OrdenEmitter
	if err := json.Unmarshal(entry.Value(), &o); err != nil {
		return nil, fmt.Errorf("valor del emitter inválido en %q: %w", code, err)
	}
	return &o, nil
}

// Cargar lee el snapshot del silo. Devuelve nil si no hay nada guardado.
func (k *KV) Cargar(code string) (*model.Snapshot, error) {
	entry, err := k.kv.Get(code)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var snap model.Snapshot
	if err := json.Unmarshal(entry.Value(), &snap); err != nil {
		return nil, fmt.Errorf("valor inválido en %q: %w", code, err)
	}
	return &snap, nil
}

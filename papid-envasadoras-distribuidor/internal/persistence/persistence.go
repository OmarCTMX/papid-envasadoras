// Package persistence guarda en NATS JetStream KV el último estado de proceso
// de cada envasadora, para que un dashboard que arranque (o reconecte) vea
// datos de inmediato sin esperar el próximo mensaje del PLC.
//
//	Bucket: "papid_envasadoras_proceso"
//	Clave:  "<machine_code>"   (ej. "envasadora-1")
//	Valor:  JSON de model.Envasadora
//
// El distribuidor es el único escritor. Es opcional: si el KV no está
// disponible, el distribuidor sigue repartiendo (solo pierde la restauración).
package persistence

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"

	"papid-envasadoras-distribuidor/internal/model"
)

const bucketName = "papid_envasadoras_proceso"

// KV envuelve el acceso al bucket.
type KV struct {
	kv nats.KeyValue
}

// New abre (o crea) el bucket. El distribuidor es el dueño, así que lo crea si
// no existe, con un TTL amplio para que no se acumulen claves de máquinas
// retiradas.
func New(nc *nats.Conn) (*KV, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("jetstream: %w", err)
	}
	kv, err := js.KeyValue(bucketName)
	if err != nil {
		kv, err = js.CreateKeyValue(&nats.KeyValueConfig{
			Bucket:      bucketName,
			Description: "Último estado de proceso por envasadora (distribuidor)",
			History:     1,
			TTL:         24 * time.Hour,
		})
		if err != nil {
			return nil, fmt.Errorf("crear bucket %q: %w", bucketName, err)
		}
	}
	return &KV{kv: kv}, nil
}

// Guardar persiste el estado de una envasadora.
func (k *KV) Guardar(e model.Envasadora) error {
	if e.MachineCode == "" {
		return nil
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = k.kv.Put(e.MachineCode, data)
	return err
}

// Cargar lee el último estado de una envasadora. Devuelve nil si no hay dato.
func (k *KV) Cargar(code string) (*model.Envasadora, error) {
	entry, err := k.kv.Get(code)
	if err != nil {
		if err == nats.ErrKeyNotFound {
			return nil, nil
		}
		return nil, err
	}
	var e model.Envasadora
	if err := json.Unmarshal(entry.Value(), &e); err != nil {
		return nil, fmt.Errorf("valor inválido en %q: %w", code, err)
	}
	return &e, nil
}

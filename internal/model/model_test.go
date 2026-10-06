package model

import (
	"encoding/json"
	"testing"
)

// TestNormalizarRellena verifica que un estado con menos de 3 valvuladoras se
// rellene hasta 3.
func TestNormalizarRellena(t *testing.T) {
	e := Estado{Valvuladoras: []Valvuladora{{Peso: 50}}}
	e.Normalizar(3)
	if len(e.Valvuladoras) != 3 {
		t.Fatalf("esperaba %d valvuladoras, obtuve %d", 3, len(e.Valvuladoras))
	}
	if e.Valvuladoras[0].Peso != 50 {
		t.Errorf("la primera valvuladora debía conservar peso 50, obtuve %v", e.Valvuladoras[0].Peso)
	}
}

// TestNormalizarRecorta verifica que un estado con más de 3 valvuladoras se
// recorte a 3.
func TestNormalizarRecorta(t *testing.T) {
	e := Estado{Valvuladoras: make([]Valvuladora, 5)}
	e.Normalizar(3)
	if len(e.Valvuladoras) != 3 {
		t.Fatalf("esperaba %d valvuladoras, obtuve %d", 3, len(e.Valvuladoras))
	}
}

// TestNormalizarPeso verifica que los pesos negativos se lleven a 0 y que un
// peso mayor al setpoint NO se acote (el tope visual lo pone el frontend con
// peso/setpoint).
func TestNormalizarPeso(t *testing.T) {
	e := Estado{Valvuladoras: []Valvuladora{{Peso: 150, Setpoint: 20}, {Peso: -20}, {Peso: 75}}}
	e.Normalizar(3)
	if e.Valvuladoras[0].Peso != 150 {
		t.Errorf("peso 150 debía conservarse (sin tope), obtuve %v", e.Valvuladoras[0].Peso)
	}
	if e.Valvuladoras[1].Peso != 0 {
		t.Errorf("peso -20 debía llevarse a 0, obtuve %v", e.Valvuladoras[1].Peso)
	}
	if e.Valvuladoras[2].Peso != 75 {
		t.Errorf("peso 75 debía conservarse, obtuve %v", e.Valvuladoras[2].Peso)
	}
}

// TestUnmarshalContrato verifica que el JSON del contrato acordado se deserializa
// a los campos correctos.
func TestUnmarshalContrato(t *testing.T) {
	raw := `{"machine_code":"envasadora-1","of":"955086","item_code":"PESP",
	"item_name":"COT. Pulido Espejo Blanco Pegaduro 10 kg","peso_producto":10,
	"lote_actual":1,"lotes_totales":10,
	"valvuladoras":[{"peso":7,"setpoint":20,"columna":5},{"peso":5,"setpoint":20,"columna":5},
	{"peso":9,"setpoint":22,"columna":6}]}`
	var e Estado
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatalf("no debía fallar el unmarshal: %v", err)
	}
	if e.OF != "955086" || e.ItemCode != "PESP" || e.LoteActual != 1 || e.LotesTotales != 10 {
		t.Errorf("campos del header mal deserializados: %+v", e)
	}
	if e.PesoProducto != 10 {
		t.Errorf("peso_producto mal deserializado: %v", e.PesoProducto)
	}
	if len(e.Valvuladoras) != 3 || e.Valvuladoras[2].Setpoint != 22 || e.Valvuladoras[2].Columna != 6 {
		t.Errorf("valvuladoras mal deserializadas: %+v", e.Valvuladoras)
	}
}

// TestRegistrosIDFlexible verifica que el ID del bulto se acepte como texto o
// como número, y que bultos/registros se deserialicen.
func TestRegistrosIDFlexible(t *testing.T) {
	raw := `{"valvuladoras":[{"peso":3,"bultos":1000,"registros":[
		{"id":"B-12","peso":10.02,"fecha":"2026-09-30T11:27:32-06:00"},
		{"id":11,"peso":9.98,"fecha":"2026-09-30T11:26:10-06:00"}]}]}`
	var e Estado
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatalf("no debía fallar el unmarshal: %v", err)
	}
	v := e.Valvuladoras[0]
	if v.Bultos != 1000 {
		t.Errorf("bultos: esperaba 1000, obtuve %d", v.Bultos)
	}
	if len(v.Registros) != 2 || v.Registros[0].ID != "B-12" || v.Registros[1].ID != "11" {
		t.Errorf("registros mal deserializados: %+v", v.Registros)
	}
}

// TestNormalizarRegistros verifica el tope de MaxRegistros (se conservan los
// primeros, que son los más nuevos) y que nunca queden en nil.
func TestNormalizarRegistros(t *testing.T) {
	regs := make([]Registro, MaxRegistros+5)
	for i := range regs {
		regs[i].ID = FlexString(string(rune('a' + i%26)))
	}
	e := Estado{Valvuladoras: []Valvuladora{{Registros: regs, Bultos: -3}}}
	e.Normalizar(3)
	if len(e.Valvuladoras[0].Registros) != MaxRegistros {
		t.Errorf("esperaba %d registros, obtuve %d", MaxRegistros, len(e.Valvuladoras[0].Registros))
	}
	if e.Valvuladoras[0].Registros[0].ID != "a" {
		t.Errorf("debía conservar los primeros (más nuevos)")
	}
	if e.Valvuladoras[0].Bultos != 0 {
		t.Errorf("bultos negativos debían llevarse a 0")
	}
	if e.Valvuladoras[1].Registros == nil {
		t.Errorf("las valvuladoras rellenadas debían traer registros vacíos, no nil")
	}
}

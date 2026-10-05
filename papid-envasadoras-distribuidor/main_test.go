package main

import "testing"

// TestDecodBlobObjeto verifica la forma { "envasadoras": [...] }.
func TestDecodBlobObjeto(t *testing.T) {
	raw := `{"envasadoras":[
		{"machine_code":"envasadora-1","of":"111","peso_producto":10,
		 "valvuladoras":[{"peso":7,"setpoint":20,"columna":5,
		   "leds":{"paro":false,"limpieza":false,"ciclo":true},"bultos":3}]},
		{"machine_code":"envasadora-2","of":"222","peso_producto":25,"valvuladoras":[]}
	]}`
	envs, err := decodBlob([]byte(raw))
	if err != nil {
		t.Fatalf("no debía fallar: %v", err)
	}
	if len(envs) != 2 {
		t.Fatalf("esperaba 2 envasadoras, obtuve %d", len(envs))
	}
	if envs[0].MachineCode != "envasadora-1" || envs[0].Valvuladoras[0].Bultos != 3 {
		t.Errorf("envasadora-1 mal decodificada: %+v", envs[0])
	}
	if envs[1].MachineCode != "envasadora-2" || envs[1].PesoProducto != 25 {
		t.Errorf("envasadora-2 mal decodificada: %+v", envs[1])
	}
}

// TestDecodBlobArreglo verifica la forma de arreglo directo [...].
func TestDecodBlobArreglo(t *testing.T) {
	raw := `[{"machine_code":"envasadora-3","of":"333","valvuladoras":[]}]`
	envs, err := decodBlob([]byte(raw))
	if err != nil {
		t.Fatalf("no debía fallar: %v", err)
	}
	if len(envs) != 1 || envs[0].MachineCode != "envasadora-3" {
		t.Errorf("arreglo mal decodificado: %+v", envs)
	}
}

// TestDecodBlobInvalido verifica que un JSON basura devuelva error.
func TestDecodBlobInvalido(t *testing.T) {
	if _, err := decodBlob([]byte("no soy json")); err == nil {
		t.Error("esperaba error con JSON inválido")
	}
}

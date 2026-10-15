package personal

import "testing"

func TestClampSlots(t *testing.T) {
	casos := map[int]int{0: DefaultSlots, -1: DefaultSlots, 1: 1, 2: 2, 3: 3, 7: 3}
	for in, want := range casos {
		if got := ClampSlots(in); got != want {
			t.Errorf("ClampSlots(%d) = %d, esperaba %d", in, got, want)
		}
	}
}

func TestAplicarSyncEstados(t *testing.T) {
	st := New(3)
	avisos := 0
	st.SetOnChange(func() { avisos++ })

	st.AplicarSync([]PersonaSync{
		{Nombre: "Omar Ramirez", Rol: "Envasador", Status: "Activo"},
		{Nombre: "Michel Davalos", Rol: "", Status: "Inactivo"},
	})
	s := st.Slots()
	if s[0].Estado != EstadoActive || s[0].Nombre != "Omar Ramirez" {
		t.Errorf("slot 0: %+v", s[0])
	}
	if s[1].Estado != EstadoPending || s[1].Rol != "—" {
		t.Errorf("slot 1 (Inactivo, sin rol): %+v", s[1])
	}
	if s[2].Estado != EstadoEmpty {
		t.Errorf("slot 2 debía quedar vacante: %+v", s[2])
	}
	if avisos != 1 {
		t.Errorf("esperaba 1 aviso, hubo %d", avisos)
	}

	// Mismo contenido: no debe volver a avisar.
	st.AplicarSync([]PersonaSync{
		{Nombre: "Omar Ramirez", Rol: "Envasador", Status: "Activo"},
		{Nombre: "Michel Davalos", Rol: "", Status: "Inactivo"},
	})
	if avisos != 1 {
		t.Errorf("sin cambios no debía avisar (avisos=%d)", avisos)
	}

	// Unsigned (lista vacía): todo vacante.
	st.AplicarSync(nil)
	for i, sl := range st.Slots() {
		if sl.Estado != EstadoEmpty {
			t.Errorf("slot %d debía quedar vacante tras unsigned: %+v", i, sl)
		}
	}
}

func TestAplicarSyncIgnoraSobrantes(t *testing.T) {
	st := New(1)
	st.AplicarSync([]PersonaSync{{Nombre: "A", Status: "Activo"}, {Nombre: "B", Status: "Activo"}})
	if n := len(st.Slots()); n != 1 {
		t.Fatalf("esperaba 1 slot, hay %d", n)
	}
}

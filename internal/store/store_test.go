package store

import (
	"testing"

	"papid-envasadoras/internal/model"
)

// proceso arma un mensaje del PLC con el contador de bultos y el peso de cada
// envasadora.
func proceso(bultos []int, pesos []float64) model.Estado {
	e := model.Estado{}
	for i := range bultos {
		e.Valvuladoras = append(e.Valvuladoras, model.Valvuladora{Bultos: bultos[i], Peso: pesos[i]})
	}
	return e
}

func ordenPrueba() model.Orden {
	return model.Orden{DocNum: "955430", ItemName: "Pegapiedra", PesoProducto: 20, NoLotes: 6, CantidadBts: 3}
}

// TestBultosSeSumanDesdeLaOrden verifica que solo cuenten los bultos hechos
// DESPUÉS de cargar la orden (el contador del PLC no tiene que reiniciarse) y
// que los lotes salgan de la suma de las envasadoras.
func TestBultosSeSumanDesdeLaOrden(t *testing.T) {
	st := New("B2-A-silo-1", 2)

	// El PLC ya venía contando antes de la orden: 100 y 50 bultos.
	st.AplicarProceso(proceso([]int{100, 50}, []float64{0, 0}))
	st.AplicarOrden(ordenPrueba(), []model.Trabajador{{Nombre: "Omar Ramirez", Rol: "Envasador"}})

	if e := st.Estado(); e.BultosOrden != 0 || e.LoteActual != 0 {
		t.Fatalf("al cargar la orden todo debe empezar en 0: %+v", e)
	}

	// Envasadora 1 llena hasta 20.1 kg y completa un bulto; la 2 completa dos.
	st.AplicarProceso(proceso([]int{100, 50}, []float64{20.1, 5}))
	st.AplicarProceso(proceso([]int{101, 52}, []float64{0.5, 0.3}))

	e := st.Estado()
	if e.Valvuladoras[0].Bultos != 1 || e.Valvuladoras[1].Bultos != 2 {
		t.Fatalf("bultos por envasadora mal: %d, %d", e.Valvuladoras[0].Bultos, e.Valvuladoras[1].Bultos)
	}
	if e.BultosOrden != 3 || e.LoteActual != 1 {
		t.Fatalf("3 bultos con 3 por lote = 1 lote; obtuve bultos=%d lote=%d", e.BultosOrden, e.LoteActual)
	}

	// La tabla de la envasadora 1 registra el peso máximo del ciclo (20.1).
	regs := e.Valvuladoras[0].Registros
	if len(regs) != 1 || regs[0].Peso != 20.1 || regs[0].ID != "1" || regs[0].Fecha == "" {
		t.Fatalf("registro de la envasadora 1 mal: %+v", regs)
	}
	if len(e.Valvuladoras[1].Registros) != 2 {
		t.Fatalf("la envasadora 2 debía tener 2 filas: %+v", e.Valvuladoras[1].Registros)
	}
	if len(e.Trabajadores) != 1 || e.Trabajadores[0].Rol != "Envasador" {
		t.Fatalf("personal mal: %+v", e.Trabajadores)
	}
}

// TestIDyPesoDelPLC verifica que si el PLC manda id_bulto y peso_bulto, la
// tabla los usa.
func TestIDyPesoDelPLC(t *testing.T) {
	st := New("B2-A-silo-1", 2)
	st.AplicarProceso(proceso([]int{0, 0}, []float64{0, 0}))
	st.AplicarOrden(ordenPrueba(), nil)

	e := proceso([]int{1, 0}, []float64{0, 0})
	e.Valvuladoras[0].IDBulto = "B-7781"
	e.Valvuladoras[0].PesoBulto = 20.04
	st.AplicarProceso(e)

	r := st.Estado().Valvuladoras[0].Registros
	if len(r) != 1 || r[0].ID != "B-7781" || r[0].Peso != 20.04 {
		t.Fatalf("debía usar id/peso del PLC: %+v", r)
	}
}

// TestOrdenTerminadaYTope verifica que no se pase de noLotes y que se marque
// la orden como terminada.
func TestOrdenTerminadaYTope(t *testing.T) {
	st := New("B2-A-silo-1", 2)
	st.AplicarProceso(proceso([]int{0, 0}, []float64{0, 0}))
	st.AplicarOrden(ordenPrueba(), nil) // 6 lotes de 3 bultos = 18

	st.AplicarProceso(proceso([]int{10, 12}, []float64{0, 0})) // 22 bultos
	e := st.Estado()
	if e.LoteActual != 6 || !e.OrdenTerminada {
		t.Fatalf("22 bultos con 6x3: debía ser 6/6 terminada; obtuve %d/%d terminada=%v",
			e.LoteActual, e.LotesTotales, e.OrdenTerminada)
	}
}

// TestOrdenNuevaReinicia verifica que una orden nueva reinicie bultos y tabla.
func TestOrdenNuevaReinicia(t *testing.T) {
	st := New("B2-A-silo-1", 2)
	st.AplicarProceso(proceso([]int{0, 0}, []float64{0, 0}))
	st.AplicarOrden(ordenPrueba(), nil)
	st.AplicarProceso(proceso([]int{4, 0}, []float64{0, 0}))

	st.AplicarOrden(ordenPrueba(), nil)
	e := st.Estado()
	if e.BultosOrden != 0 || len(e.Valvuladoras[0].Registros) != 0 {
		t.Fatalf("la orden nueva debía empezar en cero: %+v", e)
	}
	// Sigue contando desde el valor actual del PLC (4), no desde 0.
	st.AplicarProceso(proceso([]int{5, 0}, []float64{0, 0}))
	if b := st.Estado().BultosOrden; b != 1 {
		t.Fatalf("tras la orden nueva, 4→5 es 1 bulto; obtuve %d", b)
	}
}

// TestReinicioDelPLC verifica que si el contador del PLC baja (reinicio), no se
// pierdan ni se resten bultos.
func TestReinicioDelPLC(t *testing.T) {
	st := New("B2-A-silo-1", 1)
	st.AplicarProceso(proceso([]int{50}, []float64{0}))
	st.AplicarOrden(ordenPrueba(), nil)
	st.AplicarProceso(proceso([]int{52}, []float64{0})) // +2
	st.AplicarProceso(proceso([]int{1}, []float64{0}))  // el PLC se reinició: +1
	if b := st.Estado().BultosOrden; b != 3 {
		t.Fatalf("esperaba 3 bultos, obtuve %d", b)
	}
}

// TestSinOrdenNoCuenta verifica que sin orden no se cuenten bultos.
func TestSinOrdenNoCuenta(t *testing.T) {
	st := New("B2-A-silo-1", 1)
	st.AplicarProceso(proceso([]int{1}, []float64{0}))
	st.AplicarProceso(proceso([]int{5}, []float64{0}))
	if b := st.Estado().BultosOrden; b != 0 {
		t.Fatalf("sin orden no se cuentan bultos; obtuve %d", b)
	}
}

// TestRestaurar verifica que el snapshot del KV regrese orden y contadores, y
// que no pise una orden que llegó por POST antes.
func TestRestaurar(t *testing.T) {
	origen := New("B2-A-silo-1", 2)
	origen.AplicarProceso(proceso([]int{0, 0}, []float64{0, 0}))
	origen.AplicarOrden(ordenPrueba(), []model.Trabajador{{Nombre: "Omar"}})
	origen.AplicarProceso(proceso([]int{2, 1}, []float64{0, 0}))
	snap := origen.Snapshot()

	nuevo := New("B2-A-silo-1", 2)
	if !nuevo.Restaurar(snap) {
		t.Fatal("debía restaurar")
	}
	e := nuevo.Estado()
	if e.OF != "955430" || e.BultosOrden != 3 || len(e.Trabajadores) != 1 {
		t.Fatalf("restauración incompleta: %+v", e)
	}
	// Sigue contando desde el último valor del PLC guardado (2 y 1).
	nuevo.AplicarProceso(proceso([]int{3, 1}, []float64{0, 0}))
	if b := nuevo.Estado().BultosOrden; b != 4 {
		t.Fatalf("tras restaurar, 2→3 suma 1: esperaba 4, obtuve %d", b)
	}

	if nuevo.Restaurar(snap) {
		t.Fatal("con una orden ya cargada no debía restaurar encima")
	}
}

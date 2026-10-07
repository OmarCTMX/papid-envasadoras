package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"papid-envasadoras/internal/store"
)

// ordenAdmin es el JSON real del admin (con materiales, que se ignoran) más el
// personal.
const ordenAdmin = `{
  "docEntry": 231527, "docNum": 955430, "fecha": "2026-10-05T00:00:00",
  "itemCode": "PPIE", "itemName": "COT. Pegapiedra Gris Pegaduro 20 kg",
  "pesoProducto": 20, "noLotes": 6, "cantidadBts": 118, "factorProductividad": 1,
  "maquina": "B2 (A) 1", "estatus": "Liberada",
  "materiales": [{"almacen":"10","cantidadPorLote":9600,"itemCode":"ZACM18-1600"}],
  "personal": [{"nombre":"Omar Ramirez","rol":"Envasador"},{"name":"Michel Davalos","tipo_asignacion":"Envasador"}]
}`

func servidor(token string) (*httptest.Server, *store.Store) {
	st := store.New("B2-A-silo-1", 2)
	mux := http.NewServeMux()
	New(st, token, nil).Registrar(mux)
	return httptest.NewServer(mux), st
}

func post(t *testing.T, url, body, token string) (*http.Response, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url+"/api/orden", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST falló: %v", err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res, out
}

func TestPostOrdenCarga(t *testing.T) {
	srv, st := servidor("")
	defer srv.Close()

	res, out := post(t, srv.URL, ordenAdmin, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("esperaba 200, obtuve %d: %v", res.StatusCode, out)
	}
	e := st.Estado()
	if e.OF != "955430" || e.ItemName != "COT. Pegapiedra Gris Pegaduro 20 kg" ||
		e.LotesTotales != 6 || e.BultosPorLote != 118 || e.PesoProducto != 20 {
		t.Fatalf("orden mal aplicada: %+v", e)
	}
	if len(e.Trabajadores) != 2 || e.Trabajadores[1].Nombre != "Michel Davalos" || e.Trabajadores[1].Rol != "Envasador" {
		t.Fatalf("personal mal aplicado (incluye formato name/tipo_asignacion): %+v", e.Trabajadores)
	}
}

func TestPostOrdenOtraMaquina(t *testing.T) {
	srv, st := servidor("")
	defer srv.Close()

	body := strings.Replace(ordenAdmin, `"B2 (A) 1"`, `"B2 (A) 3"`, 1)
	res, _ := post(t, srv.URL, body, "")
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("una orden de otro silo debía dar 409, obtuve %d", res.StatusCode)
	}
	if st.TieneOrden() {
		t.Fatal("no debía cargar la orden de otro silo")
	}
}

func TestPostOrdenValidacion(t *testing.T) {
	srv, _ := servidor("")
	defer srv.Close()

	res, out := post(t, srv.URL, `{"docNum": 1, "itemName": "x"}`, "")
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("faltando noLotes/cantidadBts/pesoProducto debía dar 400, obtuve %d", res.StatusCode)
	}
	if msg, _ := out["mensaje"].(string); !strings.Contains(msg, "cantidadBts") {
		t.Fatalf("el mensaje debía decir qué falta: %q", msg)
	}
	if res, _ := post(t, srv.URL, `no soy json`, ""); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("JSON inválido debía dar 400, obtuve %d", res.StatusCode)
	}
}

func TestPostOrdenToken(t *testing.T) {
	srv, st := servidor("secreto")
	defer srv.Close()

	if res, _ := post(t, srv.URL, ordenAdmin, ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("sin token debía dar 401, obtuve %d", res.StatusCode)
	}
	if res, _ := post(t, srv.URL, ordenAdmin, "otro"); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("token incorrecto debía dar 401, obtuve %d", res.StatusCode)
	}
	if res, _ := post(t, srv.URL, ordenAdmin, "secreto"); res.StatusCode != http.StatusOK {
		t.Fatalf("token correcto debía dar 200, obtuve %d", res.StatusCode)
	}
	if !st.TieneOrden() {
		t.Fatal("con token correcto debía cargar la orden")
	}
}

func TestGetYDeleteOrden(t *testing.T) {
	srv, st := servidor("")
	defer srv.Close()
	post(t, srv.URL, ordenAdmin, "")

	res, err := http.Get(srv.URL + "/api/orden")
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/orden falló: %v %v", err, res)
	}
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	res.Body.Close()
	avance, _ := out["avance"].(map[string]any)
	if avance["bultos_por_lote"].(float64) != 118 || avance["lotes_totales"].(float64) != 6 {
		t.Fatalf("avance mal: %v", avance)
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/orden", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("DELETE falló: %v %v", err, res)
	}
	res.Body.Close()
	if st.TieneOrden() {
		t.Fatal("tras DELETE no debía haber orden")
	}
}

func TestGetControl(t *testing.T) {
	srv, _ := servidor("")
	defer srv.Close()
	post(t, srv.URL, ordenAdmin, "")

	res, err := http.Get(srv.URL + "/api/control")
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/control falló: %v %v", err, res)
	}
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	res.Body.Close()

	if out["machine_code"] != "B2-A-silo-1" {
		t.Errorf("machine_code mal: %v", out["machine_code"])
	}
	// Sin Control (nil), el emitter no está disponible.
	em, _ := out["emitter"].(map[string]any)
	if em == nil || em["disponible"] != false {
		t.Errorf("emitter debía venir no disponible: %v", em)
	}
	// La orden de la API sí está (se cargó por POST).
	api, _ := out["api"].(map[string]any)
	if api == nil || api["orden"] == nil {
		t.Errorf("la orden de la API debía estar presente: %v", api)
	}
}

func TestDeleteOrdenEmitterSinControl(t *testing.T) {
	srv, _ := servidor("") // ctrl = nil
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/orden/emitter", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE falló: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("sin NATS debía dar 503, obtuve %d", res.StatusCode)
	}
}

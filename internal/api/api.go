// Package api expone la API REST del dashboard del silo:
//
//	POST   /api/orden   → el admin carga la orden + personal (reinicia contadores)
//	GET    /api/orden   → orden actual, personal y avance (bultos / lotes)
//	DELETE /api/orden   → quita la orden (pantalla en blanco, contadores en cero)
//	GET    /api/estado  → estado completo que ve la pantalla
//	GET    /docs        → documentación interactiva (Scalar, sin internet)
//
// Si API_TOKEN está definido, POST y DELETE exigen el header
// "Authorization: Bearer <token>" (o "X-API-Token: <token>"). Sin token, la
// API queda abierta a cualquiera en la red.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"papid-envasadoras/internal/model"
	"papid-envasadoras/internal/store"
)

// maxCuerpo limita el tamaño del POST (la orden real pesa unos pocos KB).
const maxCuerpo = 1 << 20 // 1 MB

// maxPersonal acota cuántas personas se aceptan por orden.
const maxPersonal = 20

// API agrupa las dependencias de los handlers.
type API struct {
	st    *store.Store
	token string
}

// New crea la API. token vacío = sin autenticación.
func New(st *store.Store, token string) *API {
	return &API{st: st, token: strings.TrimSpace(token)}
}

// Registrar monta las rutas en el mux.
func (a *API) Registrar(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/orden", a.autenticado(a.postOrden))
	mux.HandleFunc("GET /api/orden", a.getOrden)
	mux.HandleFunc("DELETE /api/orden", a.autenticado(a.deleteOrden))
	mux.HandleFunc("GET /api/estado", a.getEstado)
	mux.HandleFunc("GET /api/openapi.json", a.openapi)
	mux.HandleFunc("GET /docs", a.docs)
}

// --- Cuerpo del POST ---

// peticionOrden es el JSON que manda el admin. Solo se leen los campos que usa
// la pantalla; el resto (por ejemplo "materiales") se ignora.
type peticionOrden struct {
	DocEntry            model.FlexString `json:"docEntry"`
	DocNum              model.FlexString `json:"docNum"`
	Fecha               string           `json:"fecha"`
	ItemCode            string           `json:"itemCode"`
	ItemName            string           `json:"itemName"`
	PesoProducto        model.FlexNum    `json:"pesoProducto"`
	NoLotes             model.FlexNum    `json:"noLotes"`
	CantidadBts         model.FlexNum    `json:"cantidadBts"`
	FactorProductividad model.FlexNum    `json:"factorProductividad"`
	Maquina             string           `json:"maquina"`
	Estatus             string           `json:"estatus"`
	Personal            []personaOrden   `json:"personal"`
}

// personaOrden acepta los nombres de campo en español o en el formato del
// signed (name / tipo_asignacion), para no atar al admin a uno solo.
type personaOrden struct {
	Nombre         string `json:"nombre"`
	Name           string `json:"name"`
	Rol            string `json:"rol"`
	TipoAsignacion string `json:"tipo_asignacion"`
	EmployeeID     string `json:"employee_id"`
}

func (a *API) postOrden(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCuerpo)
	var p peticionOrden
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		apiError(w, http.StatusBadRequest, "JSON inválido: "+err.Error())
		return
	}

	// --- Validaciones ---
	var faltan []string
	if strings.TrimSpace(string(p.DocNum)) == "" {
		faltan = append(faltan, "docNum")
	}
	if strings.TrimSpace(p.ItemName) == "" {
		faltan = append(faltan, "itemName")
	}
	if p.NoLotes <= 0 {
		faltan = append(faltan, "noLotes (> 0)")
	}
	if p.CantidadBts <= 0 {
		faltan = append(faltan, "cantidadBts (> 0)")
	}
	if p.PesoProducto <= 0 {
		faltan = append(faltan, "pesoProducto (> 0)")
	}
	if len(faltan) > 0 {
		apiError(w, http.StatusBadRequest, "faltan o son inválidos: "+strings.Join(faltan, ", "))
		return
	}
	if len(p.Personal) > maxPersonal {
		apiError(w, http.StatusBadRequest, fmt.Sprintf("máximo %d personas por orden", maxPersonal))
		return
	}

	// La orden debe ser de ESTE silo: "B2 (A) 3" → "B2-A-silo-3". Así una
	// orden mandada a la NUC equivocada se rechaza en vez de mostrarse mal.
	if p.Maquina != "" {
		codigo := model.CodigoDeMaquina(p.Maquina)
		if codigo == "" {
			apiError(w, http.StatusBadRequest,
				fmt.Sprintf("no se entiende la máquina %q (formato esperado: \"B2 (A) 1\")", p.Maquina))
			return
		}
		if !strings.EqualFold(codigo, a.st.MachineCode()) {
			apiError(w, http.StatusConflict,
				fmt.Sprintf("la orden es para %q (%s) y este dashboard es %q",
					p.Maquina, codigo, a.st.MachineCode()))
			return
		}
	}

	personal := make([]model.Trabajador, 0, len(p.Personal))
	for _, per := range p.Personal {
		nombre := primero(per.Nombre, per.Name)
		if nombre == "" {
			continue
		}
		personal = append(personal, model.Trabajador{
			Nombre:     nombre,
			Rol:        primero(per.Rol, per.TipoAsignacion),
			EmployeeID: strings.TrimSpace(per.EmployeeID),
		})
	}

	orden := model.Orden{
		DocEntry:            p.DocEntry,
		DocNum:              model.FlexString(strings.TrimSpace(string(p.DocNum))),
		Fecha:               p.Fecha,
		ItemCode:            strings.TrimSpace(p.ItemCode),
		ItemName:            strings.TrimSpace(p.ItemName),
		PesoProducto:        float64(p.PesoProducto),
		NoLotes:             int(p.NoLotes),
		CantidadBts:         int(p.CantidadBts),
		FactorProductividad: float64(p.FactorProductividad),
		Maquina:             p.Maquina,
		Estatus:             p.Estatus,
		Recibida:            time.Now().Format(time.RFC3339),
	}
	a.st.AplicarOrden(orden, personal)
	log.Printf("[api] Orden %s cargada: %q, %d lotes de %d bultos, %d personas",
		orden.DocNum, orden.ItemName, orden.NoLotes, orden.CantidadBts, len(personal))

	escribirJSON(w, http.StatusOK, a.respuestaOrden("Orden cargada"))
}

func (a *API) getOrden(w http.ResponseWriter, r *http.Request) {
	escribirJSON(w, http.StatusOK, a.respuestaOrden(""))
}

func (a *API) deleteOrden(w http.ResponseWriter, r *http.Request) {
	a.st.QuitarOrden()
	log.Printf("[api] Orden quitada")
	escribirJSON(w, http.StatusOK, a.respuestaOrden("Orden quitada"))
}

func (a *API) getEstado(w http.ResponseWriter, r *http.Request) {
	escribirJSON(w, http.StatusOK, a.st.Estado())
}

// respuestaOrden arma la respuesta de /api/orden: la orden, el personal y el
// avance calculado.
func (a *API) respuestaOrden(mensaje string) map[string]any {
	orden, personal := a.st.Orden()
	e := a.st.Estado()
	bultosPorEnvasadora := make([]int, len(e.Valvuladoras))
	for i, v := range e.Valvuladoras {
		bultosPorEnvasadora[i] = v.Bultos
	}
	res := map[string]any{
		"machine_code": a.st.MachineCode(),
		"orden":        orden, // null si no hay orden
		"personal":     personal,
		"avance": map[string]any{
			"bultos_orden":          e.BultosOrden,
			"bultos_por_envasadora": bultosPorEnvasadora,
			"bultos_por_lote":       e.BultosPorLote,
			"lotes_completados":     e.LoteActual,
			"lotes_totales":         e.LotesTotales,
			"orden_terminada":       e.OrdenTerminada,
		},
	}
	if mensaje != "" {
		res["ok"] = true
		res["mensaje"] = mensaje
	}
	return res
}

// --- Autenticación ---

// autenticado exige el token (si está configurado) antes de llamar al handler.
func (a *API) autenticado(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.token == "" {
			h(w, r)
			return
		}
		recibido := strings.TrimSpace(r.Header.Get("X-API-Token"))
		if auth := r.Header.Get("Authorization"); recibido == "" && strings.HasPrefix(auth, "Bearer ") {
			recibido = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		}
		// Comparación en tiempo constante (no filtra el token por timing).
		if subtle.ConstantTimeCompare([]byte(recibido), []byte(a.token)) != 1 {
			apiError(w, http.StatusUnauthorized, "token inválido o ausente")
			return
		}
		h(w, r)
	}
}

// --- Documentación ---

// openapi sirve la spec desde static/openapi.json (editable sin recompilar).
func (a *API) openapi(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	http.ServeFile(w, r, "internal/dashboard/web/static/openapi.json")
}

// docs sirve la UI de Scalar (vendorizada en static/vendor, sin CDN).
func (a *API) docs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(scalarHTML))
}

const scalarHTML = `<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>API Envasadoras - Documentación</title>
</head>
<body>
    <div id="app"></div>
    <script src="/static/vendor/scalar.api-reference.js"></script>
    <script>
        Scalar.createApiReference('#app', { url: '/api/openapi.json', theme: 'purple' })
    </script>
</body>
</html>`

// --- Helpers ---

func primero(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func escribirJSON(w http.ResponseWriter, codigo int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(codigo)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[api] Error escribiendo la respuesta: %v", err)
	}
}

func apiError(w http.ResponseWriter, codigo int, mensaje string) {
	escribirJSON(w, codigo, map[string]any{"ok": false, "mensaje": mensaje})
}

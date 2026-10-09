// Package render arma la página inicial y el JSON de estado que se empuja por
// SSE al navegador. El dibujo de las bolas (liquidFill) lo hace ECharts en el
// cliente; aquí solo se serializan los datos.
package render

import (
	"bytes"
	"encoding/json"
	"html/template"
	"log"

	"papid-envasadoras/internal/model"
)

// Renderer agrupa las plantillas.
type Renderer struct {
	index *template.Template
}

// New carga las plantillas desde el directorio dado.
func New(dirTemplates string) (*Renderer, error) {
	tpl, err := template.New("index.html").ParseFiles(dirTemplates + "/index.html")
	if err != nil {
		return nil, err
	}
	return &Renderer{index: tpl}, nil
}

// DatosIndex son los valores que se inyectan en la plantilla al cargar la página.
type DatosIndex struct {
	Titulo         string   // título de la pestaña del navegador
	Maquina        string   // nombre visible de la envasadora (footer)
	Version        string   // marca de versión para invalidar el caché del CSS
	NumEnvasadoras int      // cuántas bolas/envasadoras muestra este silo (2 o 3)
	Codigos        []string // identificador de cada bola (CODIGOS_ENVASADORAS), en orden
}

// Index renderiza la página completa.
func (r *Renderer) Index(d DatosIndex) (string, error) {
	var buf bytes.Buffer
	if err := r.index.Execute(&buf, d); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// EstadoJSON serializa el estado actual para empujarlo por SSE. El navegador lo
// parsea y actualiza las bolas y las etiquetas sin recargar la página.
func (r *Renderer) EstadoJSON(e model.Estado) string {
	data, err := json.Marshal(e)
	if err != nil {
		log.Printf("[render] Error serializando estado: %v", err)
		return "{}"
	}
	return string(data)
}

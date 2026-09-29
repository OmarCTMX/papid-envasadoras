// Package sse implementa un broker simple de Server-Sent Events.
// Mantiene la lista de clientes conectados y les empuja eventos con nombre.
package sse

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// heartbeatCada es el intervalo del comentario keep-alive que se manda a cada
// cliente. Sirve para detectar conexiones muertas (una pantalla apagada o con
// la red caída) y para que los proxies no corten el stream por inactividad.
const heartbeatCada = 20 * time.Second

// writeTimeout es el plazo máximo para escribir en un cliente. Sin esto, un
// cliente que dejó de leer bloqueaba la goroutine para siempre (fuga).
const writeTimeout = 10 * time.Second

// Evento es un mensaje con nombre.
type Evento struct {
	Nombre string
	Data   string
}

// Broker reparte eventos a todos los clientes SSE conectados.
type Broker struct {
	mu        sync.Mutex
	clients   map[chan Evento]bool
	onConnect func() Evento // si está definido, se envía al nuevo cliente al conectarse
}

func New() *Broker {
	return &Broker{clients: make(map[chan Evento]bool)}
}

// SetOnConnect registra el evento que se envía inmediatamente a cada nuevo
// cliente SSE al conectarse (para enviar el estado actual en el refresh).
func (b *Broker) SetOnConnect(fn func() Evento) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onConnect = fn
}

// Clientes devuelve cuántos navegadores están conectados (para diagnóstico).
func (b *Broker) Clientes() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.clients)
}

// Publicar envía un evento a todos los clientes conectados.
func (b *Broker) Publicar(ev Evento) {
	b.mu.Lock()
	total := len(b.clients)
	enviados := 0
	for ch := range b.clients {
		select {
		case ch <- ev:
			enviados++
		default:
			// Si el cliente va lento, no lo bloqueamos: se descarta este
			// evento para él. El siguiente cambio lo pondrá al día.
		}
	}
	b.mu.Unlock()
	log.Printf("[sse] Evento %q enviado a %d/%d clientes", ev.Nombre, enviados, total)
}

// Handler es el endpoint /events: mantiene viva la conexión SSE de un cliente.
func (b *Broker) Handler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming no soportado", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := make(chan Evento, 16)
	b.mu.Lock()
	b.clients[ch] = true
	onConnect := b.onConnect
	total := len(b.clients)
	b.mu.Unlock()
	log.Printf("[sse] Cliente conectado (total: %d) desde %s", total, r.RemoteAddr)

	// La baja se registra INMEDIATAMENTE después del alta: si algo entre medio
	// falla o entra en panic, el cliente no se queda colgado en el mapa.
	defer func() {
		b.mu.Lock()
		delete(b.clients, ch)
		restantes := len(b.clients)
		close(ch)
		b.mu.Unlock()
		log.Printf("[sse] Cliente desconectado (quedan: %d) desde %s", restantes, r.RemoteAddr)
		if rec := recover(); rec != nil {
			log.Printf("[sse] panic en el handler (recuperado): %v", rec)
		}
	}()

	// rc permite poner deadline de escritura por operación (Go 1.20+).
	rc := http.NewResponseController(w)

	escribir := func(ev Evento) bool {
		// Sin deadline, un cliente que no lee dejaría esta goroutine bloqueada
		// indefinidamente y el canal nunca se liberaría.
		_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := writeEvento(w, ev); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	// Si hay un evento inicial configurado, se lo enviamos al nuevo cliente
	// de inmediato para que vea el estado actual sin esperar el próximo cambio.
	if onConnect != nil {
		if ev := onConnect(); ev.Nombre != "" {
			if !escribir(ev) {
				return
			}
		}
	}

	latido := time.NewTicker(heartbeatCada)
	defer latido.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			if !escribir(ev) {
				return // cliente caído o lento: cerramos y liberamos
			}
		case <-latido.C:
			// Comentario SSE (línea que empieza con ':'): el navegador lo
			// ignora, pero si el cliente ya no existe la escritura falla y
			// así detectamos la conexión muerta.
			_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
		}
	}
}

// writeEvento escribe un evento SSE con el formato estándar:
//
//	event: <nombre>
//	data: <linea>
//	(línea en blanco)
func writeEvento(w http.ResponseWriter, ev Evento) error {
	var sb strings.Builder
	if ev.Nombre != "" {
		sb.WriteString("event: ")
		sb.WriteString(ev.Nombre)
		sb.WriteString("\n")
	}
	// El data puede tener varias líneas; cada una lleva su prefijo "data:".
	for _, linea := range strings.Split(ev.Data, "\n") {
		sb.WriteString("data: ")
		sb.WriteString(linea)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	_, err := fmt.Fprint(w, sb.String())
	return err
}

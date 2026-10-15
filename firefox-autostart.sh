#!/bin/bash

# ============================================================
#  Autostart del Dashboard de Envasadoras
# ------------------------------------------------------------
#  Abre UNA sola ventana de Firefox en kiosko con el dashboard
#  de envasadoras (puerto 3001).
#
#  Ya NO abre el Dashboard Personal ni cambia de ventana: el
#  carrusel del propio dashboard muestra/oculta la pantalla de
#  personal según haya orden y personal registrado. Por eso se
#  eliminó el script change_window y el aviso a Node-RED.
# ============================================================

# ============================================================
#  Logging
# ------------------------------------------------------------
#  El autostart de GNOME no da terminal, así que la salida se
#  perdería. La redirigimos a un archivo dentro del home (no
#  requiere permisos de root) con timestamp, para revisar
#  errores tras un arranque. Al correr el script a mano, la
#  salida sigue saliendo por consola gracias a tee.
# ============================================================
LOG_DIR="$HOME/.local/share/papid/envase/logs"
mkdir -p "$LOG_DIR" 2>/dev/null
LOG_FILE="$LOG_DIR/firefox-autostart.$(date +%Y-%m-%d_%H%M%S).log"
exec > >(tee -a "$LOG_FILE") 2>&1
echo "===== $(date -Iseconds) — inicio de firefox-autostart (PID $$) ====="

wait_for_page() {
    local url="$1"
    local timeout=120   # Max wait time in seconds
    local elapsed=0

    echo "Checking $url ..."
    while [ $elapsed -lt $timeout ]; do
        status=$(curl -s -o /dev/null -w "%{http_code}" "$url")
        if [ "$status" -eq 200 ]; then
            echo "✅ $url is ready!"
            return 0
        else
            echo "❌ $url returned $status, retrying in 5s... ($elapsed/$timeout)"
            sleep 5
            elapsed=$((elapsed + 5))
        fi
    done
    echo "⏰ Timeout: $url did not respond with 200 after $timeout seconds."
    return 1
}

show_error_popup() {
    local message="$1"
    echo "⚠️ Showing popup: $message"
    # Show a modal dialog for 60 seconds
    zenity --error \
        --title="Error de conexión" \
        --width=500 --height=200 \
        --text="$message" &
    popup_pid=$!
    sleep 60
    kill "$popup_pid" 2>/dev/null
}

# --- ÚNICA VENTANA: Dashboard de Envasadoras (puerto 3001) ---
echo "⏳ Esperando el dashboard de envasadoras (http://localhost:3001/)..."
if wait_for_page "http://localhost:3001/"; then
    firefox --new-window --kiosk "http://localhost:3001/"
else
    show_error_popup "No se pudo abrir la pantalla de envasadoras.\nFavor de contactar con operaciones."
fi

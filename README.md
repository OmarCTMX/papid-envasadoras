# Dashboard de Envasadoras — Go + SSE + NATS

Visualización en tiempo real de **una envasadora** con **tres valvuladoras**.
Cada valvuladora se dibuja como una bola de líquido ([echarts-liquidfill]) que
se llena de 0 a 100 según el **peso** que recibe. El número dentro de la bola
es el peso directo (no un porcentaje). Debajo de cada bola se muestran el
**setpoint** y la **columna** (kg).

Arriba: la orden de fabricación (**OF**, item code, item name) y el **lote**
(`n / lotes`). Abajo: el mismo footer que el dashboard de personal (logo,
indicador de conexión en vivo/offline y reloj).

> Este proyecto es **solo el frontend/dashboard**: consume el estado desde NATS
> y lo muestra. No publica nada. El setpoint y la columna se cambian desde
> **Node-RED** (con sus tres botones: subir, bajar y alternar el valor a
> modificar); el dashboard solo refleja el valor que llega.

[echarts-liquidfill]: https://github.com/ecomfe/echarts-liquidfill

---

## Arquitectura

```
Node-RED / PLC ──► NATS ──► [Dashboard]  ──SSE──►  Navegador
                 papid.envasadora.<MACHINE_CODE>     (3 bolas liquidFill)
```

- El dashboard se **suscribe** a `papid.envasadora.<MACHINE_CODE>`.
- Cada mensaje reemplaza el estado en memoria y se empuja a los navegadores
  por Server-Sent Events (evento `estado`).
- El navegador redibuja las bolas y las etiquetas sin recargar la página.
- ECharts y el plugin liquidFill están **vendorizados** en `/static/vendor`,
  así el dashboard funciona **sin internet** (importante para las NUC en piso).

---

## Requisitos

- Go 1.26 o superior
- Un servidor NATS accesible (la IP va en el `.env`)

---

## Configuración (.env)

Copia `.env.example` a `.env` y ajusta:

```dotenv
REGION=Cotija                     # se muestra en el título de la pestaña
MAQUINA=Envasadora-1              # nombre visible en el centro del footer
MACHINE_CODE=envasadora-1        # define el subject: papid.envasadora.envasadora-1
PORT=3000
NATS_URL=nats://localhost:4222   # IP del NATS de las envasadoras
NATS_USER=papid
NATS_PASS=papid2024
```

Para apuntar a otra envasadora basta cambiar `MACHINE_CODE` (y `MAQUINA` para
el rótulo) y reiniciar.

---

## Correr

```powershell
# Local (desde la raíz del proyecto)
go run ./cmd/dashboard
```

| Ruta | Descripción |
|------|-------------|
| `http://localhost:3000/` | Dashboard de la envasadora |
| `http://localhost:3000/events` | Stream SSE (uso interno del navegador) |
| `http://localhost:3000/static/...` | Assets (CSS, fuente, ECharts, logo) |

> **Ejecuta siempre desde la raíz del proyecto**: las rutas de los assets
> (`internal/dashboard/web/...`) son relativas al directorio de trabajo.

### Con Docker

```powershell
docker build -t dashboard-envasadoras .
docker run --rm --env-file .env -p 3000:3000 dashboard-envasadoras
```

---

## Contrato del mensaje NATS

Subject: `papid.envasadora.<MACHINE_CODE>` (ej. `papid.envasadora.envasadora-1`).

Payload JSON:

```json
{
  "machine_code": "envasadora-1",
  "of": "133871",
  "item_code": "TXT-DUR-20",
  "item_name": "Texturizado Premium Durazno 20 kg",
  "lote_actual": 1,
  "lotes_totales": 7,
  "valvuladoras": [
    { "peso": 60, "setpoint": 20, "columna": 5 },
    { "peso": 45, "setpoint": 20, "columna": 5 },
    { "peso": 80, "setpoint": 22, "columna": 6 }
  ]
}
```

Notas:

- `peso` va de 0 a 100. El dashboard acota los valores fuera de rango y siempre
  muestra exactamente **3 valvuladoras** (rellena o recorta si vienen de más o
  de menos), así un mensaje mal formado no rompe la UI.
- `machine_code` es opcional en el payload; si viene y no coincide con el del
  `.env`, el mensaje se descarta.
- El dashboard solo **lee**. `setpoint` y `columna` llegan ya calculados desde
  Node-RED.

---

## Probar sin PLC (con el CLI de NATS)

```powershell
# Ver lo que llega al subject
nats sub papid.envasadora.envasadora-1 --server nats://localhost:4222 --user papid --password papid2024

# Publicar un estado de prueba (guarda el JSON en un archivo para evitar
# problemas de comillas en PowerShell y publícalo así):
$json = Get-Content -Raw .\ejemplo.json
nats pub papid.envasadora.envasadora-1 $json --server nats://localhost:4222 --user papid --password papid2024
```

Con Docker, para levantar un NATS de pruebas rápido:

```powershell
docker run -d --rm --name nats-test -p 4222:4222 nats:2-alpine --user papid --pass papid2024
```

---

## Estructura del proyecto

```
papid_envasadoras/
├── cmd/
│   └── dashboard/main.go       # Arranque: config, NATS, SSE, HTTP, apagado
├── .env                        # Configuración (no se sube a git)
├── go.mod / go.sum             # Dependencias (godotenv + nats.go)
├── Dockerfile
│
└── internal/
    ├── model/        # Estado de la envasadora y las valvuladoras (+ tests)
    ├── store/        # Estado en memoria + notificación de cambios
    ├── natsclient/   # Conexión y suscripción a NATS (solo consumidor)
    ├── sse/          # Broker de Server-Sent Events
    ├── render/       # Página inicial + JSON de estado para SSE
    └── dashboard/
        └── web/
            ├── templates/index.html   # Estructura + JS (ECharts liquidFill + SSE)
            └── static/                # CSS, fuente, logo y vendor/ (ECharts)
```

---

## Actualizar ECharts vendorizado

```powershell
# ECharts 5 (compatible con echarts-liquidfill 3)
curl -o internal/dashboard/web/static/vendor/echarts.min.js `
  https://cdn.jsdelivr.net/npm/echarts@5.6.0/dist/echarts.min.js

curl -o internal/dashboard/web/static/vendor/echarts-liquidfill.min.js `
  https://cdn.jsdelivr.net/npm/echarts-liquidfill@3.1.0/dist/echarts-liquidfill.min.js
```

> `echarts-liquidfill@3` requiere `echarts@5`. No mezclar con `echarts@6`.

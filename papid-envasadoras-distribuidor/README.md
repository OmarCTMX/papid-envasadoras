# papid-envasadoras-distribuidor

Reparte los datos de **proceso** de las envasadoras. Resuelve el problema de las
conexiones al PLC: en vez de que cada envasadora/dashboard se conecte al PLC
(N conexiones), hay **una sola** conexión (Node-RED con un nodo S7) que lee todo
el PLC y publica un único mensaje; este servicio lo parte y lo reparte.

```
            UNA conexión S7
 PLC ───────────────────────▶ Node-RED (1 nodo s7)
                                     │ publica el "blob" con TODAS las envasadoras
                                     ▼
                          papid.envasadoras.plc        (NATS)
                                     │
                                     ▼
                        ┌───────────────────────┐
                        │     DISTRIBUIDOR       │  parte por machine_code
                        └───┬───────┬───────┬────┘
                            ▼       ▼       ▼
          papid.envasadora.envasadora-1  .2  .3   (lo que escucha cada dashboard)
                            ▼       ▼       ▼
                      dashboard-1   -2      -3
```

El PLC solo ve a Node-RED. Todo lo demás vive en NATS (bus), que soporta las N
máquinas y los N dashboards sin tocar al PLC. **Escalable:** agregar una
envasadora no requiere cambios aquí; basta con que venga en el blob.

> Esto es SOLO proceso (peso, setpoint, columna, leds, bultos, registros). El
> **personal** (asignaciones, tarjetas) lo maneja el `papid-envasadoras-emitter`,
> que es otro servicio y no se conecta al PLC.

## Qué hace

```
Escucha:  papid.envasadoras.plc        (el blob con todas las envasadoras)
Publica:  papid.envasadora.<machine_code>   (una por envasadora)
Persiste: NATS KV  bucket papid_envasadoras_proceso  (clave = machine_code)
```

El KV guarda el último estado de cada envasadora para que un dashboard que
arranque (o reconecte) vea datos de inmediato sin esperar al PLC.

## Contrato del blob (entrada)

Node-RED publica en `papid.envasadoras.plc`. Se aceptan **dos formas** (usa la
que te sea cómoda en Node-RED):

Forma objeto:

```json
{
  "envasadoras": [
    {
      "machine_code": "envasadora-1",
      "of": "955086",
      "item_code": "PESP",
      "item_name": "COT. Pulido Espejo Blanco Pegaduro 10 kg",
      "peso_producto": 10,
      "lote_actual": 1,
      "lotes_totales": 10,
      "valvuladoras": [
        { "peso": 7, "setpoint": 20, "columna": 5,
          "leds": { "paro": false, "limpieza": false, "ciclo": true },
          "bultos": 124,
          "registros": [ { "id": "124", "peso": 10.02, "fecha": "2026-10-05T12:00:00-06:00" } ] }
      ]
    },
    { "machine_code": "envasadora-2", "...": "..." }
  ]
}
```

Forma arreglo (equivalente):

```json
[ { "machine_code": "envasadora-1", "...": "..." }, { "machine_code": "envasadora-2", "...": "..." } ]
```

Cada objeto de `envasadoras` es **exactamente** el contrato que el dashboard
consume; el distribuidor lo publica tal cual en `papid.envasadora.<machine_code>`.
Una envasadora sin `machine_code` se ignora (no se sabría a qué subject va).

## Configuración (.env)

```dotenv
SUBJECT_BLOB=papid.envasadoras.plc   # subject de entrada (default)
NATS_URL=nats://localhost:4222
NATS_USER=papid
NATS_PASS=papid2024
```

## Correr

```powershell
go run .
# o Docker:
docker build -t papid-envasadoras-distribuidor .
docker run --rm --env-file .env papid-envasadoras-distribuidor
```

## Probar sin PLC (con el CLI de NATS)

```powershell
# Guarda el blob en blob.json y publícalo:
Get-Content -Raw .\blob.json | nats pub papid.envasadoras.plc --force-stdin --server nats://localhost:4222 --user papid --password papid2024

# Ver lo que sale para una envasadora:
nats sub papid.envasadora.envasadora-1 --server nats://localhost:4222 --user papid --password papid2024
```

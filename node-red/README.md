# Flow de pruebas (Node-RED) — Dashboard de Envasadoras

`flow-pruebas-envasadora.json` alimenta al dashboard publicando el estado de la
envasadora por NATS en `papid.envasadora.<MACHINE_CODE>`.

Está dividido en **tres zonas independientes** para que pasar de simulación a
producción sea trivial: quitas la simulación y habilitas la entrada real, sin
tocar el publicador ni el dashboard.

```
 ZONA A · SIMULACIÓN          ZONA C · SALIDA (común)
 injects ─► Simulador ─┐
                       ├─► Fijar subject + validar ─► publicar NATS ─► dashboard
 ZONA B · ENTRADA REAL ┘                              └► debug
 (NATS/MQTT in) ─► Normalizar
```

- **A · Simulación:** botones inject → *Simulador*. Genera el contrato de
  prueba. Es lo único que se quita/desactiva en producción.
- **B · Entrada real:** un nodo *NATS in* (viene **deshabilitado**) → *Normalizar
  entrada*. Cuando llegue el dato real, se habilita y alimenta el mismo
  publicador.
- **C · Salida:** *Fijar subject + validar* → *publicar NATS* + *debug*. Común a
  las dos. Fija el subject por `machine_code` y valida antes de mandar.

## Requisitos

1. **Node-RED** corriendo.
2. Nodo NATS: `node-red-contrib-nats-suite`
   (**≡ → Manage palette → Install**).
3. Un **servidor NATS**. Para pruebas con Docker:
   ```powershell
   docker run -d --rm --name nats-test -p 4222:4222 nats:2-alpine --user papid --pass papid2024
   ```
4. El **dashboard** corriendo (`go run ./cmd/dashboard`) contra el mismo NATS y
   con el mismo `MACHINE_CODE`.

## Importar y configurar

- **≡ → Import** → `flow-pruebas-envasadora.json`.
- En los nodos NATS (server, publicar, entrada) ajusta URL/usuario/contraseña.
- Si tu `MACHINE_CODE` no es `envasadora-1`, cámbialo en el `machine_code` del
  nodo *Simulador*. El publicador ya usa `msg.topic` (que fija la zona C según
  el `machine_code`), así que el subject se ajusta solo.

## Usar (simulación)

Cada botón manda un comando (`msg.topic`) al Simulador:

| Botón | Qué hace |
|-------|----------|
| **Cargar OF / lote** | Reinicia los pesos a 0 (orden y lote se quedan). Corre solo al desplegar. |
| **Simular peso ▶ (1s)** | Cada segundo sube el peso hacia el **setpoint** de cada valvuladora y reinicia al llegar. **El peso nunca pasa del setpoint**, así la bola no se rebasa (el llenado es peso/setpoint). |
| **Subir ▲ / Bajar ▼** | Cambian en 1 el valor seleccionado (setpoint o columna). Si al bajar el setpoint queda por debajo del peso, el peso se recorta para no rebasar. |
| **Cambiar valor ⇄** | Alterna entre editar **setpoint** y **columna**. El valor activo se ve en el `node.status`. |

## Pasar a producción

1. Desactiva los injects de la **zona A** (o borra la zona).
2. Habilita el nodo **entrada real (NATS in)** de la zona B (clic derecho →
   Enable) y ajusta su `subject`/`topic` a tu fuente.
3. Ajusta **Normalizar entrada**: si tu fuente ya manda el contrato exacto, el
   passthrough lo deja pasar; si no, usa el mapeo de ejemplo comentado.
4. Publicador y dashboard no cambian.

## Contrato

Subject: `papid.envasadora.<MACHINE_CODE>`

```json
{
  "machine_code": "envasadora-1",
  "of": "955086",
  "item_code": "PESP",
  "item_name": "COT. Pulido Espejo Blanco Pegaduro 10 kg",
  "lote_actual": 1,
  "lotes_totales": 10,
  "valvuladoras": [
    { "peso": 10, "setpoint": 20, "columna": 5 },
    { "peso": 20, "setpoint": 20, "columna": 5 },
    { "peso": 8,  "setpoint": 8,  "columna": 6 }
  ]
}
```

- El **llenado de cada bola es `peso / setpoint`**: el setpoint es el 100%.
- El **número dentro de la bola es el peso** directo (en kg), no un porcentaje.
- Un peso mayor al setpoint llena la bola al 100% (no se desborda).

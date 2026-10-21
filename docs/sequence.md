# Diagrama de secuencia

Flujo de `POST /characters`: validación de entrada, consulta a DragonBall API y persistencia en MongoDB. El cliente puede ser el cliente de consola o cualquier consumidor HTTP.

[Volver al README](../README.md#diagrama-de-secuencia).

![Diagrama de secuencia del servicio DragonBall](sequence.png)

## Fuente Mermaid

```mermaid
sequenceDiagram
    actor Cliente
    participant Servicio as Servicio Go
    participant API as DragonBall API
    participant Mongo as MongoDB
    Cliente->>Servicio: POST /characters {name}
    Servicio->>Servicio: Validar Content-Type y JSON
    alt Entrada inválida
        Servicio-->>Cliente: 400 / 413 / 415
    else Entrada válida
        Servicio->>API: GET /api/characters?name=...
        API-->>Servicio: Respuesta externa
        Servicio->>Servicio: Resolver name o character y validar campos
        alt Sin coincidencia o fallo externo
            Servicio-->>Cliente: 404 / 502 / 504
        else Coincidencia válida y única
            Servicio->>Mongo: InsertOne con ID externo como _id
            Mongo-->>Servicio: Confirmación o error
            alt Escritura confirmada
                Servicio->>Servicio: Registrar creación en logs
                Servicio-->>Cliente: 201 Created con personaje
            else Duplicado o fallo de almacenamiento
                Servicio-->>Cliente: 409 / 503 / 504
            end
        end
    end
```

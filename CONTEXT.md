# Dozzle Gaia Context

Dozzle Gaia observes containers across one or more hosts. This glossary keeps host, container, and grouping language precise.

## Language

**Host**:
A server or agent connection that provides access to containers.
_Avoid_: Server, node when referring to the UI concept.

**Host Group**:
A named grouping of hosts, commonly used to separate environments such as Development, Stage, and Production.
_Avoid_: Environment label, server label.

## Relationships

- A **Host Group** contains zero or more **Hosts**.
- A **Host** belongs to zero or one **Host Group**.

## Example Dialogue

> **Dev:** "Should production be an environment label on each container?"
> **Domain expert:** "No, production is a **Host Group** when it identifies the server or agent the containers come from."

## Flagged Ambiguities

- "development, stage, production labels" was used to mean labels for the servers that containers come from; resolved: these are **Host Group** names.

# Dozzle Gaia Context

Dozzle Gaia observes containers across one or more hosts. This glossary keeps host, container, and grouping language precise.

## Language

**Host**:
A server or agent connection that provides access to containers.
_Avoid_: Server, node when referring to the UI concept.

**Host Group**:
A named grouping of hosts, commonly used to separate environments such as Development, Stage, and Production.
_Avoid_: Environment label, server label.

**Alert**:
A rule that watches selected containers and produces a notification when a log, metric, or lifecycle event condition is met.
_Avoid_: Notification rule when discussing the user-facing concept.

**Notification Destination**:
An external target that receives alert notifications, such as a webhook, ntfy, or Dozzle Cloud.
_Avoid_: Dispatcher when discussing the user-facing concept.

**Notification Content**:
The human-readable title and message body delivered by a **Notification Destination**.
_Avoid_: Do not use content to mean routing fields such as topic, priority, or tags.

**Quiet Hours**:
A daily time window where alerts are held by default and only break through after repetition or an explicit bypass.
_Avoid_: Do not use quiet hours to mean general low-traffic periods unless an alert policy is active.

**Quiet-Hours Tag**:
A notification marker showing that an alert was captured during **Quiet Hours**.
_Avoid_: Do not use it to mean the alert was suppressed.

**Quiet-Hours Bypass**:
An explicit alert policy that allows an alert to be delivered during **Quiet Hours**.
_Avoid_: Do not use notification priority to mean quiet-hours bypass.

**Burst Escalation**:
A priority or routing escalation caused by the same alert triggering repeatedly within a configured window.
_Avoid_: Traffic burst when referring specifically to notification priority escalation.

**Pair Alert**:
A log alert that starts a timer on a trigger message and only notifies if a matching follow-up or clear message does not arrive in time.
_Avoid_: Watchdog when discussing the user-facing concept.

**Hold Window**:
A short delay before an alert is delivered. It does not define a clear condition by itself.
_Avoid_: Do not use this to describe the trigger/clear matching of a **Pair Alert**.

## Relationships

- A **Host Group** contains zero or more **Hosts**.
- A **Host** belongs to zero or one **Host Group**.
- An **Alert** sends notifications to one **Notification Destination**.
- A **Notification Destination** may define **Notification Content** separately from routing.
- **Quiet Hours** can apply globally or be overridden for a single **Alert**.
- **Quiet-Hours Bypass** can break through **Quiet Hours**.
- Notification priority does not bypass **Quiet Hours** by itself.
- **Burst Escalation** does not bypass **Quiet Hours** unless paired with an explicit **Quiet-Hours Bypass** policy.
- **Quiet Hours** take precedence over a **Hold Window**.
- Alerts are held by default during **Quiet Hours**, tagged with the **Quiet-Hours Tag**, and delivered gradually after the quiet window ends.

## Example Dialogue

> **Dev:** "Should production be an environment label on each container?"
> **Domain expert:** "No, production is a **Host Group** when it identifies the server or agent the containers come from."

> **Dev:** "Should burst traffic during quiet hours page someone?"
> **Domain expert:** "That is **Burst Escalation** during **Quiet Hours**: a repeated alert may use a higher priority or route after it is released, but it does not bypass Quiet Hours unless an explicit Quiet-Hours Bypass policy is set."

## Flagged Ambiguities

- "development, stage, production labels" was used to mean labels for the servers that containers come from; resolved: these are **Host Group** names.

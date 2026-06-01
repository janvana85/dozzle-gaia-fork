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

**Delivery Schedule**:
A weekly alert policy that defines which weekdays an **Alert** is active for notification processing.
_Avoid_: Excluded days, quiet days, cooldown.

**Alert Pause**:
An explicit state where an **Alert** is inactive either until a chosen time or indefinitely.
_Avoid_: Cooldown, quiet hours, delivery schedule.

**Burst Escalation**:
A priority or routing escalation caused by the same alert triggering repeatedly within a configured window.
_Avoid_: Traffic burst when referring specifically to notification priority escalation.

**Cooldown**:
A per-alert, per-container suppression window that prevents the same alert from firing again until the timer expires.
_Avoid_: Do not use cooldown to mean quiet hours or burst escalation.

**Pair Alert**:
A log or event alert that starts a timer on a trigger message and only notifies if a matching follow-up or clear message does not arrive in time.
_Avoid_: Watchdog when discussing the user-facing concept.

**Restart Loop**:
An event alert that confirms a container is stuck in repeated restarts before notifying, using either sustained `restarting` state or repeated `restart` events within a window.
_Avoid_: Restart storm when referring to the user-facing alert concept.

**Log Cache**:
A server-side log store on Dozzle that keeps container log history only within the configured retention window for fast browsing, search, and export even if the originating agent is unavailable.
_Avoid_: Do not use cache to mean live stream buffering only.

**Retention Window**:
The maximum age of log history that **Log Cache** may keep or backfill.
_Avoid_: Do not use retention to mean rejecting a container because older log history exists.

**Cached Log Search**:
A server-side search over **Log Cache** that returns a bounded result set instead of sending the full retained history to the browser.
_Avoid_: Do not use cached search to mean browser-side filtering of all retained logs.

**Log Backfill**:
The process that fills **Log Cache** from existing container log history within the **Retention Window**.
_Avoid_: Do not use backfill to mean live log streaming.

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
- **Delivery Schedule** makes an **Alert** inactive on non-selected weekdays.
- **Delivery Schedule** uses the alert's time context when deciding the current weekday.
- An **Alert** without an explicit **Delivery Schedule** is active on all weekdays.
- A **Delivery Schedule** must include at least one active weekday.
- **Alert Pause** makes an **Alert** inactive regardless of its **Delivery Schedule**.
- **Alert Pause** and **Delivery Schedule** are evaluated before matching, cooldown, and quiet-hours handling.
- Notification priority does not bypass **Quiet Hours** by itself.
- **Burst Escalation** does not bypass **Quiet Hours** unless paired with an explicit **Quiet-Hours Bypass** policy.
- **Cooldown** suppresses repeated alerts for the same container; it does not change quiet-hours delivery rules.
- **Quiet Hours** take precedence over a **Hold Window**.
- Alerts are held by default during **Quiet Hours**, tagged with the **Quiet-Hours Tag**, and delivered gradually after the quiet window ends.
- **Log Cache** may contain less than the **Retention Window** when the container has less available log history.
- **Log Backfill** must not block live log viewing.
- Log browsing may page backward through **Log Cache** until the **Retention Window** is reached.
- **Cached Log Search** searches only log history that exists inside **Log Cache**.
- **Cached Log Search** is scoped to the current log-viewing context unless the user opens a global search view.
- **Cached Log Search** results are a stable snapshot; new live log matches become available only after the search is refreshed.

## Example Dialogue

> **Dev:** "Should production be an environment label on each container?"
> **Domain expert:** "No, production is a **Host Group** when it identifies the server or agent the containers come from."

> **Dev:** "Should burst traffic during quiet hours page someone?"
> **Domain expert:** "That is **Burst Escalation** during **Quiet Hours**: a repeated alert may use a higher priority or route after it is released, but it does not bypass Quiet Hours unless an explicit Quiet-Hours Bypass policy is set."

> **Dev:** "Should the browser load four days of retained logs and filter them locally?"
> **Domain expert:** "No, that is **Cached Log Search**: the server searches **Log Cache** and returns bounded results for the current log-viewing context."

## Flagged Ambiguities

- "development, stage, production labels" was used to mean labels for the servers that containers come from; resolved: these are **Host Group** names.
- "cache four days" was used ambiguously; resolved: **Retention Window** limits the age of logs kept or backfilled, and older available logs are ignored rather than causing the container to be skipped.

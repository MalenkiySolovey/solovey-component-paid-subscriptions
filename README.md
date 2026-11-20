# Solovey Paid Subscriptions

Panel-only commercial components for Solovey, retaining the Product component
ID `paid-subscriptions`. This repository does not host a database, authorize
Client access, publish subscriptions, or run an independent bot service.

`provider` implements invoice delivery, provider authenticity and
reconciliation using typed purchase snapshots. The root package owns the
existing five-table schema v4 and commercial models. `store` contains commerce
queries for tariffs, orders, customer bindings, invoice recovery and refund
claims. It accepts the Panel's connection and does not open a database.

Backend owns Client quota, expiry, accounting, audit and the transaction
boundary for settlement, refund and recovery. `workflow` owns a bounded
poll/reconcile/cancel pass, invoked only by the Panel's existing scheduler.
Paid uses the supplied transaction runner so a recovered charge invokes the
entitlement owner on the same transaction as the order and cancellation
updates. Runtime refresh and notification run after commit. This package
does not start a scheduler, read Panel settings or deliver notifications.

`frontend/` owns the existing Classic and Nexus contribution and locales.
The source job checks it against the Frontend SDK recorded in
`HOST_UI_PIN.json`. It does not open a second session or HTTP client.

Adapted from `MalenkiySolovey/solovey-ui` at
`b7bdf123c1033700532f47e2f8488a03d6415bcf`,
`components/paid-subscriptions/internal/paid`, under GPL-3.0.

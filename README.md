# Solovey Paid Subscriptions

Panel-only commercial components for Solovey, retaining the Product component
ID `paid-subscriptions`. This repository does not host a database, authorize
Client access, publish subscriptions, or run an independent bot service.

`provider` implements invoice delivery, provider authenticity and
reconciliation using typed purchase snapshots. The root package owns the
existing five-table schema v4 and commercial models. `store` contains commerce
queries for tariffs, orders and customer bindings. It accepts the Panel's
connection or transaction and does not open a database.

The Panel coordinates transactions that include Client quota, expiry,
accounting or audit changes with Backend. Bot orchestration and UI are not
part of this storage slice.

Adapted from `MalenkiySolovey/solovey-ui` at
`b7bdf123c1033700532f47e2f8488a03d6415bcf`,
`components/paid-subscriptions/internal/paid`, under GPL-3.0.

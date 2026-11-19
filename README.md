# Solovey Paid Subscriptions

Panel-only commercial components for Solovey, retaining the Product component
ID `paid-subscriptions`. This repository does not host a database, authorize
Client access, publish subscriptions, or run an independent bot service.

The first slice is `provider`: invoice delivery, provider authenticity and
reconciliation using typed purchase snapshots. Solovey supplies the existing
HTTP authority and persists outcomes in the Panel transaction. Client quota,
expiry and audit writes belong to Backend. Commerce workflow and UI are not
cut over by this first slice.

Adapted from `MalenkiySolovey/solovey-ui` at
`b7bdf123c1033700532f47e2f8488a03d6415bcf`,
`components/paid-subscriptions/internal/paid/provider`, under GPL-3.0.

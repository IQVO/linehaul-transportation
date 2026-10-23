# linehaul-transportation

Retail network, bounded context **Linehaul Transportation** (Supporting, logistics tier).

Runs the middle mile: turns lanes into scheduled departures (including a pickup departure at every vendor node cutoff), keeps each departure's remaining capacity, builds loads of packages onto departures, and reports when loads depart and arrive.

Status: under construction. The design is proposed in
[retail-network-docs](https://iqvo.github.io/retail-network-docs/contexts/linehaul-transportation); `model/model.yaml` there is the single source of truth.
This repository is a service of the retail network, not of the warehouse-systems fleet; the fleet reaches the network only through the
`vendor-connectivity` Vendor API.

Runtime: Go, hexagonal, real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` are mandatory), CloudEvents 1.0 on every message
(`com.retailnetwork.logistics.linehaul-transportation.<entity>.<Event>`), transactional outbox.

See `docs/adr/` for decisions and `docs/before-first-release.md` for the packaging checklist.

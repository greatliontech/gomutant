# Plan output and confirmation progress read wrong

Field report (agent consumer), cosmetic pair:

- `--plan` prints a zeroed summary line ("0 targets …") above the real
  plan line, which reads like a result rather than a header.
- During kill confirmation the candidates counter freezes while
  confirmations tick — briefly reads as a hang. The confirmation phase
  should tick its own counter or the shared one.

Lands: cross-tool train chunk 30 (gomutant consumer-surface bounds and
visibility).

# gofresh's pin breaks darwin and BSD builds

Lands: cross-tool train chunk 324 (the gofresh bump behind 281's adoption)

gofresh v0.109.0 (014efbd, the content-keyed toolchain audit) added
closure/toolchainsource_unix.go under `//go:build unix` reading
syscall.Stat_t.Ctim — the field darwin, freebsd and netbsd spell
Ctimespec — so gomutant at its pin (v0.109.1 since 308.B) does not build
on those platforms: `GOOS=darwin go vet ./...` fails inside the
dependency. gofresh fixes it in the release after v0.109.5 (the read
split per platform spelling; its gate vets darwin, freebsd and windows
from then on). gomutant's bump to that release restores the builds; a
cross-platform vet step on gomutant's own gate (the same `pre` shape as
gofresh's) is the rider that keeps the class visible here.

Retained proof from successful GitHub Actions run40866958782, source
5dc1fc983b5e157d82ea86a6c546c0b2cb4f592c. These are byte-for-byte copies of
the previously downloaded gate.json and executed-sessions.js, verified against
the retained SHA256SUMS.json before inclusion. No measurements were regenerated.

Gate SHA256:85d1495779c37f5e19512a41922151fcebf389aefee88245b74132af744773e6
Executed client SHA256:dad72e9b2b8dd7dd9fca94bb6a0d6856d4081a49d301b53e3e133c127d01ea49

The existing checker retains duration, resource, assertion and source gates and
now anchors the gate file checksum too. This proves only historical independent
generator qualification; the actual trial still measures generator headroom.
Full raw samples remain in the workspace's generator-headroom-20261029 archive.
This is not an application or production capacity pass.

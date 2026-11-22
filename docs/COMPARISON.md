# Ecosystem Comparison

AskISO has not been benchmarked or feature-audited against other ISO 20022
tools, so this page makes no claim about them. The one comparison it makes is
measured: schema validation against libxml2, the reference implementation,
across the whole catalogue.

| Project | XSD validation | ISO 20022 business rules and scheme profiles | MT ⇄ MX conversion |
| :--- | :--- | :--- | :--- |
| **AskISO** | Pure Go; agrees with libxml2 on 4,746 of 4,746 catalogue documents | Yes | Seven MT pairs, both ways |
| `xmllint` (libxml2) | Yes, the reference | No | No |

`make differential` reproduces the validation result against your own
catalogue: AskISO and libxml2 accept the same 1,035 documents and reject the
same 3,711.

A comparison with another tool belongs here only once it has been measured or
checked against that tool's own documentation, with the source named.

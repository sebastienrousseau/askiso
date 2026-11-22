# Benchmarks & Resource Profile

## Streaming XSD Validation (`ValidateReader`)

* **Memory Complexity:** `O(max_element_below_StreamDepth)` rather than `O(document_size)`. Subtrees below `StreamDepth = 2` (repeating `CdtTrfTxInf` / `Ntry` transactions) are validated and released, leaving ~120 bytes per transaction stub.
* **Buffer Reuse:** `streamBufReaderPool` (`sync.Pool` of 64 KiB `bufio.Reader`s) eliminates repeated buffer allocations across batch and server workloads.
* **Verified Invariants:** `TestStreamingHeapDoesNotGrowWithTransactionSize` and `TestStreamingAgreesWithValidateAcrossTheCatalogue` in `internal/validator` verify bounded heap growth and verdict parity against buffered validation.

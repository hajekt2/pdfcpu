# Reading without recovery

`Configuration.NoRepair` is an opt-in, API-only reader setting. It defaults to
false and is independent of strict or relaxed validation. When enabled, the
reader allows only the two recorded free-list-head normalizations below and
refuses the first other recovery attempt with `*model.RepairRefusedError`.
`errors.As` finds it through reader error wrappers. The error contains only a
closed `model.RepairSite` code and numeric `Object` and `Offset` locations.
Object -1 means unknown; object 0 identifies a classic trailer or free-list head.
Offset -1 means unavailable, including decoded object-stream members. Other
offsets are absolute file byte offsets. Stream errors can identify the stream's
object header or data start. No error record stores document content or keys.
Encryption dictionary walkers propagate typed refusals before adding source-key
context.

The policy is frozen in the xref table when `model.NewContext` creates it. Parser
scopes and lazy member closures capture that policy. Changing the caller's
configuration after `Read` returns does not disable it. Streams also retain a
numeric refusal error for subsequent filter decoding. There is no global
tracker, mutable event slice, logger dependency or saturation state.

```go
conf := model.NewStatelessConfiguration()
conf.NoRepair = true
ctx, err := pdfcpu.Read(c, input, conf)
// Check err. Also check every subsequent lazy dereference or stream decode error.
_ = ctx
facts := ctx.NoRepairNormalizations() // Value copy; cannot change the record.
_ = facts
```

## Permitted free-list-head normalizations

Owner decision, 2026-10-21 (quote): "allow free-list normalization".
This allowance is limited to object 0. ISO 32000-1 7.5.4 reserves it as the
free-list head, with generation 65535. It is never an in-use object, so these
changes cannot change the content selected for any in-use object.

| Normalization | Recorded fact |
| --- | --- |
| Synthesize missing free object 0 without moving any other entry. | `FreeListHeadSynthesized` |
| Correct an existing free object 0's generation to 65535. | `FreeListHeadGenerationFixed` |

`ctx.NoRepairNormalizations()` returns a `model.NoRepairNormalizations` value
with only these two boolean facts. The context keeps the record private. Facts
accumulate when a normalization occurs; an already-correct head adds no fact.
They remain false when NoRepair is disabled. A synthesized head already has
generation 65535, so synthesis alone does not set the generation-correction fact.
In `postProcess`, NoRepair defers synthesis to `EnsureValidFreeList`, which records
it. The legacy path is unchanged.

Every other free-list correction still refuses: shifting object numbers to make
room for a head; reclassifying an in-use entry as free; replacing a missing link;
truncating a broken or cyclic link; resetting a nonzero head link; inserting or
linking omitted free entries; and resetting a permanently deleted entry's
nonzero link. An allowed head normalization does not authorize any of these.
`RepairFreeGeneration` remains a reserved enum value for API compatibility;
object-0 generation correction no longer returns it.

A successful initial read covers the initial phases only. A lazy member can
still refuse later. A certificate consumer must materialize every object and
stream needed for its result and check those errors before issuing a certificate.
The certificate belongs to this unchanged context and input. Validation,
optimization, page/resource consolidation and writing can change the model and
are outside the certificate lifetime. NoRepair does not make those operations
read-only. It does not establish PDF conformance, accessibility, physical revision
lineage or the validity of uninterpreted content.

## Call graph and repair inventory

The audit starts at `ReadFile` / `Read` and `model.NewContext`, then follows:

1. `readXRefTable` -> `offsetLastXRefSection` -> `buildXRefTableStartingAt` ->
   `headerVersion`, `tryXRefSection`, `parseXRefSection`, `parseXRefStreamOrRepair`.
2. Classic subsections or xref streams -> trailer parsing -> hybrid and `Prev`
   traversal -> `postProcess` -> `EnsureValidFreeList`.
3. `dereferenceXRefTable` -> `checkForEncryption` -> `decodeObjectStreams` ->
   `dereferenceObjects` -> effective root version and encryption checks.
4. `ParseObject` -> object attributes -> policy-managed recursive parser ->
   string decryption -> stream dictionary/filter pipeline -> source stream load
   -> decryption and optional filter decoding.
5. Object streams -> bounded prolog decoding -> lazy member closures ->
   `compressedObject` -> the same model parser. Later `XRefTable.Dereference`
   and reader bootstrap dereferences use these closures. Stream decoding retains
   the same refusal policy through copies, object streams and clones.

Every row below is classified as **repair**. The guard runs before the recovery
attempt or source correction. Ordinary decoding and temporary parse construction
before discovering a defect are not source repair mutations. Existing ordinary
errors, cancellation and resource-limit errors may still be returned directly.

| RepairSite | Entry points and refused operation |
| --- | --- |
| `RepairXRefReconstruction` | `bypassXrefSection` refuses before replacing the table or scanning candidates. Its callers are `parseXRefStreamOrRepair`, `repairCorruptXRefSection`, hybrid failure in `parseTrailerDict`, and `dereferenceObjects`. `readXRefTable` refuses missing-final-section reconstruction before selecting offset zero. `offsetPrev` refuses scanning for a missing previous section when `Prev` is zero. Strict-mode reconstruction is covered. Existing typed refusals survive fallback wrappers unchanged. |
| `RepairXRefSameLine` | `tryXRefSection`: taking subsection fields from the `xref` keyword's line. |
| `RepairXRefShift` | `tryXRefSection`: skipping whitespace at the declared offset, finding `xref` on a later line, or assigning `Read.RepairOffset`. Thus the later shifted-object retry in `dereferenceAndLoad` cannot run. |
| `RepairXRefEntry` | `parseXRefTableEntry` / `decodeSubsection`: stripping offset prefixes, malformed numeric widths, reclassifying in-use object zero, skipping zero or sub-header in-use offsets. `extractXRefTableEntriesFromXRefStream` also rejects type-1 object zero and zero/sub-header source offsets before inserting an entry. `parseXRefTableSubSection`: skipping short rows or digesting entries beyond the declared count. `extractXRefTableEntriesFromXRefStream`: ignoring extra records outside Index. Incomplete rows that already fail retain their error or hit the guarded reconstruction route. |
| `RepairXRefGeneration` | `parseXRefTableEntry` refuses the oversized generation that `decodeSubsection` would replace with zero. |
| `RepairXRefSize` | `xRefStreamDict` selects bounded strict Index/Size parsing under either validation mode and translates correction candidates to this site. It never reaches the relaxed Size update. `parseTrailerSize` refuses discarding invalid Size. `Read` refuses final Size replacement after the object walk. |
| `RepairXRefCycle` | `buildXRefTableStartingAt`: selecting an earlier epilog after revisiting an xref offset. |
| `RepairEOF` | `offsetLastXRefSection`: truncated EOF acceptance, non-whitespace trailing epilog bytes, percent trimming from the offset, a missing separator after startxref, or discarding an out-of-range latest offset. Missing sections that would reconstruct use `RepairXRefReconstruction`. |
| `RepairHeader` | `buildXRefTableStartingAt`: accepting a displaced header and adding its displacement to source offsets. |
| `RepairTrailerID` | `parseTrailerID`: duplicating a one-element ID array. `setupEncryptionKey`: accepting a missing encrypted-file ID. |
| `RepairFilterName` | `pdfFilterPipeline` / `normalizeStreamFilterName`: expanding inline-image-only aliases on ordinary streams. The check precedes dictionary changes. |
| `RepairFilterPipeline` | `pdfFilterPipeline` / `normalizeStreamFilterArray`: alias expansion in arrays; ignoring a non-array DecodeParms for a parameterized filter array; `singleFilter`: accepting an array in place of a single filter's parameter dictionary. |
| `RepairStreamScan` | `loadEncodedStreamContent` checks the declared extent before `readStreamContent` can enter `readStreamContentBlindly`. Missing, negative or false zero extents cannot trigger marker scanning. A valid zero-length stream is loaded as an empty byte slice directly. |
| `RepairStreamLength` | `ensureIndirectStreamLength`: missing/unresolved Length. `loadEncodedStreamContent`: forced correction or a positive extent whose following endstream marker does not match. `readStreamContent` also refuses at its EOF recovery site, and `ensureStreamLength` checks again before any source Length replacement. The check restores the shared seek position and runs before assigning Raw or changing Length. |
| `RepairStreamMarker` | `buffer`: non-EOL bytes after `stream`, before `nextStreamOffset` can tolerate spaces or use an invalid boundary. |
| `RepairPlaintextMetadata` | `decryptStreamContent` / `repairablePlaintextMetadata`: accepting valid plaintext XML after an AES ciphertext-shape failure. The guard precedes acceptance. |
| `RepairParserHex` | `parseHexLiteralOrDict`: invalid hex literal replacement with null, before recording a discard or returning a replacement. |
| `RepairParserToken` | `parseObjectValue`: an unparseable bare token becoming null. `parseObjectWithValidationPolicy` and `compressedObject`: discarding unconsumed trailing object/member tokens. |
| `RepairParserInteger` | `parseObjectValue`: an out-of-range integer becoming zero. |
| `RepairParserFallback` | `ParseObjectWithPolicy` and `model.ParseObject`: entering the repair parser after a failed first pass, including junk keys and missing-value/EOL substitution in `processDictKeys`. It refuses before the second parse. |
| `RepairParserKeyword` | `parseObjectValue`: case correction for true, false or null. |
| `RepairParserDuplicateKey` | `processDictKeys`: relaxed duplicate-key replacement, before insertion. |
| `RepairParserNumber` | `parseObjectValue`: stripping nonstandard numeric prefixes, changing a comma decimal separator or accepting a misplaced decimal sign. |
| `RepairObjectHeader` | `readObjectAttributes`: filtering junk prolog bytes, missing generation or the abbreviated bj marker accepted by `ParseObjectAttributes`. `object`: accepting a mismatched number or generation at the declared offset. |
| `RepairObjectNull` | `object`: treating an empty indirect-object body as null. |
| `RepairObjectSkipped` | `ParseObject`: swallowing EOF in relaxed mode. `dereferenceAndLoad`: skipping failed objects, before retry or acceptance. Existing typed refusals are propagated first. |
| `RepairFreeHead` | `postProcess`: shifting all entries down to create object zero. `EnsureValidFreeList`: rejecting an in-use head before normalization. Head-only synthesis and generation correction are the named allowances above. |
| `RepairFreeLink` | `validateFreeList`: choosing replacement links or truncating broken/cyclic links. `EnsureValidFreeList`: resetting a nonzero head link. `handleDanglingFree`: linking omitted free objects or fixing a permanently deleted object's nonzero link. Writer-only recycling is outside the read call graph. |
| `RepairRootVersion` | `identifyRootVersion`: accepting an invalid catalog Version, including the strict-mode silent ignore and relaxed version coercion. |
| `RepairEncryption` | `supportedEncryption` and `validateEncryptionVersion`: accepted password-entry, permission and crypt-filter representation violations. `decryptAESBytes`: accepting absent or invalid AES padding; a copy protects the source while checking padding in NoRepair mode. Standard successful decryption remains allowed. |
| `RepairObjectStreamIndex` | `buildObjectArrayForObjectStream`: accepting malformed or duplicate prolog object numbers. `decompressXRefTableEntry` compares the retained prolog number at the requested index with the xref object number before assigning the member. Superseded members that have no selected xref entry need no identity assignment. |
| `RepairFilterData` | Reader-created `StreamDict` retains this numeric error. `filter.flate.passThru` and `decodePostProcessRows` refuse unexpected EOF tolerance, including complete-row predictor output with a truncated zlib ending. `runLengthDecode.decode` and `asciiHexDecode.DecodeLength` refuse missing EOD on full decoding. ASCIIHex permits the standard odd final nibble only at EOD. The policy-managed LZW reader requires initial CLEAR, EOD, zero final padding bits, and a CLEAR or EOD when the code table is full. CCITT full decoding requires RTC/EOFB when EndOfBlock is true, which overrides Rows. EndOfBlock=false with a known row count stops at the declared rows without requiring a trailer. Existing unsupported CCITT forms remain unsupported; no damaged-row substitution is added. The error survives deferred stream or object-stream decoding. Bounded decoding that reaches its requested prefix is normal decoding. |

The reconstruction-only helpers `registerXRefObjects`, `registerXRefEntry`,
`processXRefRepairLine`, `processObject`, `parseAndLoad`, `isCatalogForRepair`
and `discardPreEncryptionObjects` are dominated by the refusal at
`bypassXrefSection`. This covers candidate scanning, malformed candidate skips,
recovered trailers, catalog discovery, entry replacement and encryption retries.
They cannot run during a NoRepair read. Lower-level context-free model/filter
APIs retain their legacy behavior unless called with the reader's policy.

## Normal decoding inventory

These paths are **normal decoding** and do not refuse merely because they
materialize values or create an internal representation.

| Path | Reason |
| --- | --- |
| Bounded header/startxref search | Locating the declared syntax is ordinary reading. Accepting a displaced header, bad latest offset or malformed epilog is guarded separately. |
| Classic, stream, hybrid, incremental and linearized traversal | Reading the selected entries and following declared Prev/XRefStm links follows PDF syntax. Newest entries supersede older entries without reconstruction. |
| Unknown xref-stream entry types | PDF 7.5.8.3 requires treating these entries as null. An explicit `XRefTableEntry.Null` marker distinguishes these placeholders from source free entries. NoRepair excludes them from free-list membership without linking or reclassifying them. Default reads retain the legacy bookkeeping. A source free-list link to a null placeholder is still a broken link and refuses. |
| Xref W defaults; compressed-object generation zero | The standard defines these implicit values. |
| Reference and bootstrap resolution | Dereferencing valid direct/indirect Length, First, N, filter and encryption entries preserves the source value. Undefined indirect references resolve to null under 7.3.10. |
| Direct PDF 2.0 Info materialization | `materializeDirectInfoDict` creates an internal indirect object for standard direct Info syntax. It allocates beyond existing objects without repairing the source. |
| Recursive parser | PDF whitespace, comments, name hex escapes, string-literal escapes, odd hex-nibble padding and dropping null-valued dictionary entries are standard syntax rules. Integer/real lookahead distinguishes references from separate numeric values. |
| Object-stream prolog and member slicing | The declared First/N/index offsets select decoded members. NUL is PDF whitespace. Comments are lexical syntax. Bounds, counts and decreasing offsets already return errors. Policy closures retain refusal through lazy parsing. |
| Standard security handler | Password authentication, key derivation, RC4/AES decryption, signature-string exclusions, Identity filters and EncryptMetadata=false follow declared security settings. Deriving a decrypted stream's Length with `ensureStreamLength` is decoding, not a replacement of the encoded source extent. |
| Filter decoding | ASCIIHex, ASCII85, LZW, Flate predictors, RunLength, CCITT and DCT decode their declared representation. Predictor defaults and CCITT Rows/Height derivation supply decoder parameters without changing the source dictionary. Prefix decoding can stop at a requested length. Malformed termination tolerance in Flate, RunLength, ASCIIHex, LZW and CCITT is guarded separately. |
| Opaque image/unsupported filters | Preserving encoded DCT/JPX/JBIG2 bytes, or leaving an unsupported stream undecoded, does not replace source bytes. A successful read does not certify every image/content codec. |
| Reference counts, caches, resource budgets and statistics | These are internal bookkeeping. Cancellation and resource-limit failures never yield a successful read. |
| Validation, content interpretation, optimization and writing | These are outside initial syntax loading and the lazy syntax decoder. Their changes invalidate a read certificate; callers must not reuse one after these operations. |

## Verification

`read_norepair_test.go` ports uacheck's control, silent stream-length replacement
and silent free-head synthesis from origin/main commit
`04a7c99a0b778912926230f34a5b1ea33884f2f4`.
The free-head regression now loads with `FreeListHeadSynthesized` recorded;
the stream-length regression still refuses and the control loads without facts.
It exercises repair families with generated public fixtures, checks default
loading, tests strict reconstruction, tests refused mutations, and checks lazy
members after read return with the caller configuration changed. Concurrent
contexts share a read-only configuration. Real existing fixtures verify hybrid
and linearized traversal; generated controls verify classic/xref/object streams,
incremental updates, indirect lengths, PDF 2.0 direct Info, RC4, AES-128 and AES-256.
Free-list tests cover both normalization facts, snapshot isolation, repeated
checks, strict and relaxed reads, unchanged default behavior, and every refused
free-list correction listed above.

Required gates:

```sh
go test ./...
go test -race ./pkg/pdfcpu/...
go vet ./...
git diff --check
```

The corpus measurement is reproducible with:

```sh
go run ./_scripts/norepair-corpus.go /path/to/public-corpus
```

A 2026-10-21 read of the frozen public corpus materialized every nonfree object.
Default strict reads loaded 527/527 files. NoRepair strict reads loaded 65/527
and refused 462/527: 126 missing free heads and 336 incorrect head generations.
There were no other errors. These are syntax-read counts, not accessibility
verdicts or a uacheck pin-change measurement. Private corpus replay belongs to
the separate integration/pin update.

After the approved object-0 normalization change on 2026-10-21, the same command
on `/home/haja/work/uacheck-corpus/public` again materialized every nonfree object.
Default strict reads still loaded 527/527. NoRepair strict reads loaded 299/527
and refused 228/527, with no other errors:

| First refusal site | Files refused / 527 total |
| --- | --- |
| `RepairXRefSize` (6) | 227 |
| `RepairFreeLink` (29) | 1 |

Among the 299 loaded NoRepair files, 234 recorded
`FreeListHeadGenerationFixed`; none recorded `FreeListHeadSynthesized`.
The remaining 65 loaded without normalization facts. Counts include facts only
for successful reads; failed reads return no context. The missing-head fixture
verifies synthesis independently. The newly reached Size and link repairs remain
refused under the unchanged policy for those sites.

All four gates passed on Linux with Go 1.26.0 on 2026-10-21.
The full-suite retry used `GOFLAGS=-p=1` and an isolated `XDG_CONFIG_HOME` under
`.git/f1b-validation/config-test`. The initial parallel run failed when shared
font-cache files disappeared during installation/reload in other test packages.
The serialized, isolated run passed; no test source was changed for this issue.
The existing parser/model and discard-log tests are retained unchanged. The
uacheck dependency pin and product proof flags are outside this fork change.
No PAC validation semantics or RE behavior claims are introduced.

## Review corrections and Size analysis, 2026-10-21

All five findings from the independent review of `ed88e44` reproduced against
`0747c45`. The corrections cover deferred codec termination, type-1 xref-stream
source offsets, object-stream member identities, encryption refusal privacy,
and standard unknown-type null resolution. `read_norepair_review_test.go`
retains the reviewer's generated reproductions. `pkg/filter/norepair_test.go`
also covers full and prefix decoding, odd ASCIIHex nibble padding, LZW final-bit
padding and table exhaustion under both EarlyChange values, valid LZW resets,
CCITT RTC/EOFB, EndOfBlock=false, byte alignment and BlackIs1. Default decoding
is checked alongside the policy-managed cases. Empty filtered streams retain
deferred decoding under NoRepair, so an empty-data cache cannot bypass required
termination checks.
These classifications follow ISO 32000-1 sections 7.4.2 (ASCIIHex), 7.4.4.2
(LZW), 7.4.5 (CCITT), 7.5.7 (object streams) and 7.5.8.3 (xref entry types).

The `RepairXRefSize` policy is unchanged. ISO 32000-1:2008
[7.5.5, Table 15](https://opensource.adobe.com/dc-acrobat-sdk-docs/pdfstandards/PDF32000_2008.pdf)
defines Size as one greater than the highest object number across the combined
xref sections. It also requires a conforming reader to ignore entries with
numbers greater than Size. Section 7.5.8.2, Table 17 gives the same meaning for
xref streams. Thus the intended numbered range ends at Size-1. The literal
ignore sentence says greater than Size; an entry exactly equal to Size already
violates the one-greater definition. The census includes this boundary explicitly
rather than treating it as conforming. No new allowance follows from this analysis.

The optional census reads the selected xref sections with the ordinary reader
and observes Size before `Read` replaces it. It classifies only files whose
first NoRepair refusal is `RepairXRefSize`. Early Size parsing or Index/Size
corrections fall into (d), rather than using an already-corrected value as the
source declaration. For a smaller Size, it materializes objects and counts
matching-generation indirect references in all selected in-use object values
and the selected trailer Root, Info, Encrypt, ID and direct Info values. This
includes references from unreachable objects. It does not interpret stream
payloads or claim document-level reachability.

On the frozen public corpus, 227 of 527 files first refused at `RepairXRefSize`:

| Subclass | Files / 227 Size refusals | Effect of correcting Size on visible in-use objects |
| --- | --- | --- |
| (a) Declared Size exceeds highest selected xref entry + 1. | 227 | Lowering the bound cannot remove a selected in-use entry: every selected entry is already below both bounds. References to absent entries still resolve to null. This is a bound correction with no change to the selected in-use object set. |
| (b) Declared Size is smaller, and present in-use objects at or above it have matching-generation references. | 0 | Raising the bound can expose previously excluded in-use objects and change reference results. Entries above Size are explicitly missing under 7.5.5; equality also violates its one-greater definition. This can change document content. |
| (c) Declared Size is smaller, but its extra objects have no such references. | 0 | Raising the bound can still change which present in-use objects a reader can enumerate or explicitly dereference. The selected graph has no observed references to them; enumeration or explicit dereferencing can still expose them. If all extra entries are free, no additional in-use object becomes visible. |
| (d) Other, including invalid Size, early Index correction, or a later model mismatch without a selected-entry bound mismatch. | 0 | The effect depends on the defect; this class supplies no general argument that correction preserves object selection. |

The other 300 files are outside this Size-refusal denominator. The public run
contains no smaller-Size examples, so (b) and (c) describe standards-based effects,
not observed changes in those public files. Case (a) alone does not authorize a
policy change. The current reader still refuses all 227 files.

Reproduce both measurements:

```sh
go run ./_scripts/norepair-corpus.go /home/haja/work/uacheck-corpus/public
PDFCPU_NOREPAIR_CORPUS=/home/haja/work/uacheck-corpus/public go test ./pkg/pdfcpu -run '^TestNoRepairXRefSizeCensus$' -count=1 -v
```

After these corrections, the full-suite command `go test -p 1 ./...`,
`go test -race -p 1 ./pkg/pdfcpu/...`, `go vet ./...` and `git diff --check`
passed on Linux with Go 1.26.0. The full and race suites used separate isolated
XDG_CONFIG_HOME directories. Generated sample changes were removed afterward.
The public corpus still loaded 527/527 by default and 299/527 under NoRepair,
with 227 Size refusals and one free-link refusal. All 1,054 strict/relaxed default
snapshots matched the independent review's prior snapshots, with zero errors or
differences. Private-corpus replay and other operating systems were not tested.
No PAC validation semantics, new dependencies or additional normalization
allowances were introduced.

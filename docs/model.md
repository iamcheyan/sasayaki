# Speech model

Sasayaki transcribes speech fully offline with **SenseVoice** (FunAudioLLM),
converted to ONNX by the sherpa-onnx project (k2-fsa). The model and the
private Python runtime are downloaded once by `sasayaki setup` into
Sasayaki's own data directory; nothing is fetched at runtime.

## Pinned artifacts

| File | Size | SHA-256 |
|---|---|---|
| `model.int8.onnx` | 239,233,841 B | `c71f0ce00bec95b07744e116345e33d8cbbe08cef896382cf907bf4b51a2cd51` |
| `tokens.txt` | 315,894 B | `f449eb28dc567533d7fa59be34e2abca8784f771850c78a47fb731a31429a1dc` |
| `LICENSE` | 71 B | `221c6df10b0931a5629adad671ea48fb7747e034c414b6d2bfa275bc3dd4ea17` |

Source:
`https://huggingface.co/csukuangfj/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17/resolve/main/`
(repo commit `2365baeacb507f821a0c8120fcee3d484dba7a07`, 2024-07-18).

> Checksum note: `model.int8.onnx` is LFS-tracked, so its Hugging Face
> object id *is* the content SHA-256. `tokens.txt` and `LICENSE` are ordinary
> git blobs; their HF ids are git-blob SHA-1s and must not be used as content
> checksums. The values above are SHA-256 of the bytes `resolve/main` serves.
> Setup and `sasayaki diagnose` verify against these values.

## Runtime

The private venv installs exactly:

```
numpy==2.5.1
sherpa-onnx==1.13.4
```

`sherpa-onnx` runs inference on CPU via ONNX Runtime. The recognizer is built
with `use_itn=True` (inverse text normalization: digits, currencies, dates)
and `num_threads=4`.

## Verified behavior (2026-08-01, aarch64 desktop)

Verified with a real venv/model in a scratch XDG tree
(`/tmp/ssk-gate`, redirected `XDG_DATA_HOME`/`XDG_RUNTIME_DIR`/…), using the
HF `test_wavs` fixtures:

| Language | Normalized transcript (engine output) | In-serve latency |
|---|---|---|
| zh | `开放时间早上9点至下午5点。` | 149 ms |
| en | `The tribal chieftain called for the boy and presented him with 50 pieces of gold.` | 105 ms |
| ja | `うちの中学は弁当制で持っていけない場合は50円の学校販売のパンを買う。` | 114 ms |
| ko | `조 금만 생각 을 하 면서 살 면 훨씬 편할 거야.` | 69 ms |
| yue | `呢几个字都表达唔到，我想讲嘅意思。` | 77 ms |

Measured paths:

- **One-shot CLI** (`engine.py transcribe …`, full Python + model start):
  ~0.69–0.73 s per utterance.
- **Warm serve worker** (production path, model resident in memory):
  69–149 ms per utterance; worker warm-up 515–532 ms.
- ITN is active: `9点` (zh), `50 pieces` (en), `50円` (ja) are normalized
  numerals. Korean output spacing is fragmented (known SenseVoice
  limitation; the text is correct).

## License

- SenseVoice model/code: **MIT** (FunAudioLLM; the HF `LICENSE` file is a
  71-byte reference to the FunASR repository license).
- sherpa-onnx (bindings + ONNX conversion): **Apache-2.0**.

Sasayaki itself is MIT (see repository `LICENSE`).

## Comparison notes

| Option | Pros | Cons | Verdict |
|---|---|---|---|
| SenseVoice int8 (this) | 5 languages, ITN, ~100 ms warm, offline | CPU-only quality below Whisper-large | **Chosen** |
| Whisper (whisper.cpp) | Stronger multilingual quality | Much larger, slower on CPU, no ITN | Not chosen |
| Cloud STT | Best quality | Network + privacy cost | Out of scope: offline is a hard requirement |

## Manual desktop smoke test

After `sasayaki setup` on a real desktop session:

1. `sasayaki status` — all runtime/model/microphone/paste rows true, worker `warm`.
2. Bind `sasayaki toggle` to a global shortcut, or run it twice from a
   terminal: first call starts recording ("Recording — press the shortcut
   again when you are done"), the second stops and reports
   "Transcribing…" then "Pasted".
3. Focus a text field before the second toggle; the transcript should appear
   in the focused application.
4. `sasayaki status` shows `last: succeeded`; `sasayaki logs` shows the
   operation and its latency.
5. Say nothing (or stop immediately): expect a specific `failed` state
   ("microphone produced an empty recording") and no clipboard write.

Re-verify an existing install at any time:

```sh
sasayaki diagnose   # checks model files against the SHA-256 table above
```

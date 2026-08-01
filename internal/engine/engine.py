#!/usr/bin/env python3
"""Private SenseVoice runtime used by Sasayaki.

Two modes:

  transcribe --model-dir DIR [--language LANG] FILE.wav
      One-shot transcription, prints normalized text on stdout.

  serve --model-dir DIR [--language LANG]
      Long-lived process speaking newline-delimited JSON over stdin/stdout.
      The model is loaded once and kept warm; a request re-creates the
      recognizer only when its language differs from the current one.

The process is owned by the Sasayaki user service. It never touches the
network and never reads or writes application state.
"""
import argparse
import json
import os
import re
import sys
import wave

REQUEST_TIMEOUT_S = 5 * 60


def read_wave(path):
    import numpy as np

    with wave.open(path, "rb") as wav:
        if wav.getnchannels() != 1 or wav.getsampwidth() != 2:
            raise SystemExit("recording must be 16-bit mono WAV")
        samples = np.frombuffer(wav.readframes(wav.getnframes()), dtype=np.int16)
        return wav.getframerate(), samples.astype("float32") / 32768.0


def clean(text):
    # SenseVoice can prefix output with language/emotion markers. Strip only
    # those control tags; user text is preserved verbatim.
    return re.sub(r"<[^>]+>", "", text).strip()


def make_recognizer(model_dir, model_file, architecture, language, num_threads=4):
    import sherpa_onnx

    if architecture == "paraformer":
        return sherpa_onnx.OfflineRecognizer.from_paraformer(
            paraformer=os.path.join(model_dir, model_file),
            tokens=os.path.join(model_dir, "tokens.txt"),
            num_threads=num_threads,
        )
    return sherpa_onnx.OfflineRecognizer.from_sense_voice(
        model=os.path.join(model_dir, model_file),
        tokens=os.path.join(model_dir, "tokens.txt"),
        language=language,
        use_itn=True,
        num_threads=num_threads,
    )


def run_recognizer(recognizer, wav):
    sample_rate, samples = read_wave(wav)
    stream = recognizer.create_stream()
    stream.accept_waveform(sample_rate, samples)
    recognizer.decode_stream(stream)
    return clean(stream.result.text)


def transcribe(args):
    recognizer = make_recognizer(args.model_dir, args.model_file, args.architecture, args.language)
    text = run_recognizer(recognizer, args.wav)
    if text:
        print(text)


def emit(obj):
    sys.stdout.write(json.dumps(obj, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def serve(args):
    recognizer = None
    language = None
    try:
        recognizer = make_recognizer(args.model_dir, args.model_file, args.architecture, args.language)
        language = args.language
    except Exception as exc:  # model missing or dependency not installed
        emit({"ready": False, "error": str(exc)})
        return 1
    emit({"ready": True, "language": language})

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except ValueError:
            emit({"ok": False, "error": "invalid request"})
            continue
        request_id = req.get("id")
        command = req.get("command")
        try:
            if command == "ping":
                emit({"id": request_id, "ok": True, "language": language})
            elif command == "transcribe":
                want = req.get("language") or language
                # Paraformer has no per-request language setting. Rebuilding
                # it is needless; SenseVoice keeps its existing language
                # switch behavior.
                if want != language and args.architecture == "sensevoice":
                    recognizer = make_recognizer(args.model_dir, args.model_file, args.architecture, want)
                    language = want
                text = run_recognizer(recognizer, req.get("wav"))
                if not text:
                    emit({"id": request_id, "ok": False, "error": "empty_speech"})
                else:
                    emit({"id": request_id, "ok": True, "text": text})
            else:
                emit({"id": request_id, "ok": False, "error": "unknown command"})
        except SystemExit as exc:
            emit({"id": request_id, "ok": False, "error": str(exc)})
        except Exception as exc:
            emit({"id": request_id, "ok": False, "error": str(exc)})
    return 0


parser = argparse.ArgumentParser()
sub = parser.add_subparsers(dest="command", required=True)
for name in ("transcribe", "serve"):
    p = sub.add_parser(name)
    p.add_argument("--model-dir", required=True)
    p.add_argument("--model-file", default="model.int8.onnx")
    p.add_argument("--architecture", choices=("sensevoice", "paraformer"), default="sensevoice")
    p.add_argument("--language", default="auto")
    if name == "transcribe":
        p.add_argument("wav")
args = parser.parse_args()
if args.command == "transcribe":
    transcribe(args)
else:
    sys.exit(serve(args))

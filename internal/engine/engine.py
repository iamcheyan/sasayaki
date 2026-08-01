#!/usr/bin/env python3
"""Small private SenseVoice command-line runtime used by Sasayaki."""
import argparse
import json
import sys


def transcribe(args):
    try:
        import sherpa_onnx
    except ImportError as exc:
        raise SystemExit("sherpa-onnx is not installed; run sasayaki setup") from exc

    import os
    recognizer = sherpa_onnx.OfflineRecognizer.from_sense_voice(
        model=os.path.join(args.model_dir, "model.int8.onnx"),
        tokens=os.path.join(args.model_dir, "tokens.txt"),
        language=args.language,
        use_itn=True,
        num_threads=4,
    )
    stream = recognizer.create_stream()
    stream.accept_wave_file(args.wav)
    recognizer.decode_stream(stream)
    print(stream.result.text.strip())


parser = argparse.ArgumentParser()
sub = parser.add_subparsers(dest="command", required=True)
p = sub.add_parser("transcribe")
p.add_argument("--model-dir", required=True)
p.add_argument("--language", default="auto")
p.add_argument("wav")
args = parser.parse_args()
if args.command == "transcribe":
    transcribe(args)

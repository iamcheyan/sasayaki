#!/usr/bin/env python3
"""Small private SenseVoice command-line runtime used by Sasayaki."""
import argparse
import re
import wave


def read_wave(path):
    import numpy as np
    with wave.open(path, "rb") as wav:
        if wav.getnchannels() != 1 or wav.getsampwidth() != 2:
            raise SystemExit("recording must be 16-bit mono WAV")
        samples = np.frombuffer(wav.readframes(wav.getnframes()), dtype=np.int16)
        return wav.getframerate(), samples.astype("float32") / 32768.0


def clean(text):
    # SenseVoice can prefix output with language/emotion markers.
    return re.sub(r"<[^>]+>", "", text).strip()


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
    sample_rate, samples = read_wave(args.wav)
    stream = recognizer.create_stream()
    stream.accept_waveform(sample_rate, samples)
    recognizer.decode_stream(stream)
    print(clean(stream.result.text))


parser = argparse.ArgumentParser()
sub = parser.add_subparsers(dest="command", required=True)
p = sub.add_parser("transcribe")
p.add_argument("--model-dir", required=True)
p.add_argument("--language", default="auto")
p.add_argument("wav")
args = parser.parse_args()
if args.command == "transcribe":
    transcribe(args)

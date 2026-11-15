"""Regenerate audio/narration.mp3 from narration.json. Each line starts 0.3 s into its 5.17 s shot.
Usage: python build_audio.py [voice] [speed]
  voice  kokoro:<voice or blend>[@accent]  e.g. "kokoro:am_santa*0.6+bm_lewis*0.4@en-gb-scotland"
         piper:<name>   a Piper preset voice, e.g. piper:en_US-john-medium  (default)
         kokoro:<name>  a Kokoro voice, e.g. kokoro:am_fenrir
  speed  1.0 = normal; higher is faster
Env: PIPER_DIR holds the Piper .onnx voices; KOKORO_DIR holds kokoro-v1.0.onnx and voices-v1.0.bin."""
import json, os, subprocess, sys, wave
from pathlib import Path

here = Path(__file__).resolve().parent
voice = sys.argv[1] if len(sys.argv) > 1 else "kokoro:am_santa*0.6+bm_lewis*0.4+pm_santa*0.25@en-us"
speed = float(sys.argv[2]) if len(sys.argv) > 2 else 1.0
lines = json.loads((here / "narration.json").read_text())
D, N = 124 / 24, len(lines)
# a shot is D long unless objectives.json gives it "seconds" (up to 6 s); S holds every shot's start time and DUR its length
objs = json.loads((here / "objectives.json").read_text())
DUR = [(objs[i].get("seconds") if i < len(objs) else None) or D for i in range(N)]
S = [sum(DUR[:i]) for i in range(N)]
(here / "audio").mkdir(exist_ok=True)
engine, name = voice.split(":", 1)

if engine == "piper":
    from piper import PiperVoice, SynthesisConfig
    model, _, spk = name.partition("@")   # piper:<model>@<speaker name or number> for multi-speaker voices
    mdir = Path(os.environ.get("PIPER_DIR", here / "piper_voices"))
    v = PiperVoice.load(mdir / f"{model}.onnx")
    smap = json.loads((mdir / f"{model}.onnx.json").read_text()).get("speaker_id_map", {})
    sid = (smap.get(spk) if spk in smap else int(spk)) if spk else None
    cfg = SynthesisConfig(length_scale=1 / speed, speaker_id=sid)
    def speak(text, path):
        text = text.replace("Adaetum", "Adaytum").replace("Gitea", "Git Tea").replace("Cloudflared", "Cloudflare dee")   # said A-DAY-tum, Git-Tea, Cloudflare-dee
        with wave.open(str(path), "wb") as w: v.synthesize_wav(text, w, syn_config=cfg)
else:
    import soundfile as sf
    from kokoro_onnx import Kokoro
    kdir = Path(os.environ.get("KOKORO_DIR", here / "models"))
    k = Kokoro(str(kdir / "kokoro-v1.0.onnx"), str(kdir / "voices-v1.0.bin"))
    import numpy as np
    def style(spec):   # "am_santa" or a blend like "am_santa*0.6+am_onyx*0.4"
        parts = [(p.split("*")[0], float(p.split("*")[1]) if "*" in p else 1.0) for p in spec.split("+")]
        return parts[0][0] and (k.get_voice_style(parts[0][0]) if len(parts) == 1 else sum(k.get_voice_style(n) * w for n, w in parts))
    spec, _, accent = name.partition("@")      # kokoro:<voice or blend>@<espeak accent>, e.g. @en-gb-scotland
    voice_arg = style(spec)
    lang = accent or ("en-gb" if spec[0] == "b" else "en-us")
    # The product name is said A-DAY-tum. The default pronunciation reads "Adaetum" as a-DEE-rum, so the phonemes are fixed after espeak has produced them.
    FIX = {"ɐdˈiːɾəm": "ɐdˈeɪtəm", "ɐdˈiːtəm": "ɐdˈeɪtəm"}
    def speak(text, path):
        text = text.replace("Gitea", "Git-Tea").replace("Cloudflared", "Cloudflare D").replace("cloudflared", "Cloudflare D")   # Git-Tea, Cloudflare-dee
        ph = k.tokenizer.phonemize(text, lang)
        for bad, good in FIX.items(): ph = ph.replace(bad, good)
        s, sr = k.create(ph, voice=voice_arg, speed=speed, lang=lang, is_phonemes=True); sf.write(path, s, sr)

cmd = ["ffmpeg", "-v", "error", "-y"]
for i, text in enumerate(lines, 1):
    f = here / f"audio/s{i:02d}.wav"; speak(text, f)
    dur = float(subprocess.check_output(["ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", str(f)]))
    print(f"shot {i:2d}  {dur:.2f}s" + ("  TOO LONG for the shot" if dur > DUR[i-1] - .3 else ""))
    cmd += ["-i", str(f)]
flt = "".join(f"[{i}:a]aresample=44100,adelay={int((S[i]+.3)*1000)}|{int((S[i]+.3)*1000)},apad[a{i}];" for i in range(N))
flt += "".join(f"[a{i}]" for i in range(N)) + f"amix=inputs={N}:normalize=0,atrim=0:{sum(DUR)}[o]"
subprocess.run(cmd + ["-filter_complex", flt, "-map", "[o]", "-ar", "44100", str(here / "audio/narration.mp3")], check=True)

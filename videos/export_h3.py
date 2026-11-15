"""Export each shot as an H3 input package: h3/shot_NNN/proxy.mp4 (124 frames, 24 fps), reference.png (first frame), prompt.txt.
Usage: python export_h3.py 1 5 13        (1-based shot numbers; needs the venv with playwright and ffmpeg on PATH)
       python export_h3.py --calib           the calibration shot (case/calib.html), written to h3/calibration/"""
import io, json, subprocess, sys, threading
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from playwright.sync_api import sync_playwright

here = Path(__file__).resolve().parent
STYLE = ("A warm, friendly fantasy temple in a gentle machine-cult style: sandstone walls, a brown and ivory marble checkerboard floor, "
         "a crimson carpet and drapes, brass trim, gold A banners, candlelight and braziers with drifting incense smoke. "
         "Hooded acolytes in lapis-blue and saffron cloaks with softly glowing eyes. Servers are stone shrines with brass bands, "
         "glowing sigils and a glowing halo. Soft cinematic lighting, stylized 3D animation. No text, captions or subtitles.")

def prompt_for(i, objectives):
    o = objectives[i]
    return f"{STYLE} {o['shows']} Keep the layout, objects and camera movement of the reference video; add natural motion and fine detail."

CALIB_PROMPT = (STYLE + " Three stone shrines stand in a row. The left shrine has a glowing red halo and one floating relic above it, the middle shrine has a green halo and two relics, "
    "the right shrine has a blue halo and three relics. A blue-cloaked acolyte walks steadily from the left side to the right side across the front of the shrines, "
    "while an orange-cloaked acolyte stands at the far right. At about one second a single gold coin arcs from a treasure chest at the far left to the blue shrine. "
    "At about two and a half seconds lightning strikes the green shrine in the middle, fire starts on its roof and smoke rises. "
    "The camera moves slowly forward and pans gently to the right. Keep the layout, objects, colours and camera movement of the reference video.")

def main():
    calib = "--calib" in sys.argv
    shots = [1] if calib else ([int(a) for a in sys.argv[1:]] or [1])
    objectives = json.loads((here / "objectives.json").read_text())
    class Q(SimpleHTTPRequestHandler):
        def log_message(self, *a): pass
    srv = ThreadingHTTPServer(("127.0.0.1", 0), partial(Q, directory=str(here))); threading.Thread(target=srv.serve_forever, daemon=True).start()
    with sync_playwright() as pw:
        b = pw.chromium.launch(args=["--use-gl=angle", "--use-angle=swiftshader", "--enable-unsafe-swiftshader"])
        p = b.new_page(viewport={"width": 960, "height": 544}); errs = []; p.on("pageerror", lambda e: errs.append(str(e)))
        p.goto(f"http://127.0.0.1:{srv.server_port}/case/" + ("calib.html" if calib else "index.html"), wait_until="networkidle"); p.wait_for_function("() => window.reconstruction && window.reconstruction.seek")
        for n in shots:
            out = here / "h3" / ("calibration" if calib else f"shot_{n:03d}"); out.mkdir(parents=True, exist_ok=True)
            ff = subprocess.Popen(["ffmpeg", "-v", "error", "-y", "-f", "image2pipe", "-vcodec", "png", "-framerate", "24", "-i", "pipe:0", "-an", "-c:v", "libx264", "-pix_fmt", "yuv420p", str(out / "proxy.mp4")], stdin=subprocess.PIPE)
            start, dur = p.evaluate("i => [window.reconstruction.starts()[i], window.reconstruction.durations()[i]]", n - 1)   # shots can be up to 6 s
            for f in range(round(dur * 24)):
                p.evaluate("t => window.reconstruction.seek(t)", start + f / 24)
                png = p.locator("canvas").screenshot(); ff.stdin.write(png)
                if f == 0: (out / "reference.png").write_bytes(png)
            ff.stdin.close(); ff.wait()
            (out / "prompt.txt").write_text((CALIB_PROMPT if calib else prompt_for(n - 1, objectives)) + "\n")
            print("exported", out)
        if errs: print("page errors:", errs[:3])
    srv.shutdown()

if __name__ == "__main__":
    main()

"""Extract independently moving installer artwork from the editable source sheet."""

from copy import deepcopy
from pathlib import Path
from xml.etree import ElementTree as ET


HERE = Path(__file__).resolve().parent
SOURCE = HERE / "adaetum-art-source.svg"
OUTPUT = HERE / "frontend" / "src" / "art"
SVG = "http://www.w3.org/2000/svg"

# Each rectangle is in source-sheet coordinates. Keep the full book faces at
# the same 2:3 proportion so PageFlip can use the art without stretching it.
PARTS = {
    "front-cover": ("front-cover", (180, 330, 700, 1050)),
    "back-cover": ("back-cover", (3100, 330, 700, 1050)),
    "inside-cover": ("inside-cover", (220, 1830, 490, 735)),
    "left-page": ("open-spread", (1120, 330, 700, 1050)),
    "right-page": ("open-spread", (1820, 330, 700, 1050)),
    "magic-fx": ("magic-fx", (2580, 610, 475, 510)),
    "lock-strap": ("lock-strap", (2440, 2013, 495, 170)),
    "lock-eye": ("lock-eye", (3180, 1998, 400, 180)),
    "lock-shackle": ("lock-shackle", (2490, 2355, 290, 265)),
    "lock-body": ("lock-body", (3200, 2360, 310, 360)),
}


def main() -> None:
    tree = ET.parse(SOURCE)
    source = tree.getroot()
    definitions = source.find(f"{{{SVG}}}defs")
    if definitions is None:
        raise ValueError("Source sheet has no shared SVG definitions")
    OUTPUT.mkdir(parents=True, exist_ok=True)
    for name, (element_id, (x, y, width, height)) in PARTS.items():
        part = source.find(f".//*[@id='{element_id}']")
        if part is None:
            raise ValueError(f"Missing source-sheet part: {element_id}")
        root = ET.Element(f"{{{SVG}}}svg", {
            "viewBox": f"{x} {y} {width} {height}",
            "width": str(width),
            "height": str(height),
        })
        root.append(deepcopy(definitions))
        root.append(deepcopy(part))
        ET.ElementTree(root).write(OUTPUT / f"{name}.svg", encoding="unicode", xml_declaration=True)


if __name__ == "__main__":
    main()

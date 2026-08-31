#!/usr/bin/env python3
"""Extract a small fixture of Administrative Code / Charter sections from the
American Legal Publishing XML export into static/editor/law_fixture.json.

The XML export is not checked in. Point --xml-dir at a local copy, e.g.:

    ./scripts/extract_law_fixture.py \
        --xml-dir ~/Downloads/intro_nyc_rules/XML \
        --section 16-497 --section 17-513 \
        --out static/editor/law_fixture.json

The fixture is the hard-coded stand-in for a future "referenced legislation"
API; the editor only ever consumes the JSON shape produced here.
"""

import argparse
import glob
import html
import json
import os
import re
import sys
import xml.etree.ElementTree as ET
from datetime import date

ALP_NOISE = re.compile(r"\[ALP [^\]]*\]")
WS = re.compile(r"\s+")

# "(L.L. 2015/004, 1/8/2015, eff. 1/1/2015; Am. L.L. 2016/091, ...; Renum. ...)"
HISTORY_NOTE = re.compile(r"^\((?:L\.L\.|Added|Am\.|Repealed|Renum\.|Ren\.)")
HISTORY_EVENT = re.compile(
    r"(?P<verb>Am\.|Repealed|Renum\.|Ren\.)?\s*L\.L\.\s*(?P<year>\d{4})/(?P<num>\d+)"
)
STATE_EVENT = re.compile(r"L\.(?P<year>\d{4})/[Cc]h\.\s*(?P<num>\d+)")

# Leading designators, in the order the drafting manual nests them (Rule 4.3).
# The XML collapses the space after a designator, so it is optional here.
DESIGNATORS = [
    ("item", re.compile(r"^\(([A-Z])\)\s*(?=\S)")),
    ("clause", re.compile(r"^\((\d+)\)\s*(?=\S)")),
    ("subparagraph", re.compile(r"^\(([a-z]{1,2})\)\s*(?=\S)")),
    ("paragraph", re.compile(r"^(\d+)\.\s*(?=[A-Z(\u201c\"])")),
    ("subdivision", re.compile(r"^([a-z]{1,2})\.\s*(?=[A-Z(\u201c\"])")),
]

EDITORIAL = re.compile(r"^(Editor's note|Editorial note|\(Repealed|\[Repealed)")

SECTION_HEADING = re.compile(r"^§\s*(?P<cite>[0-9A-Za-z\-.]+)\s*(?P<heading>.*)$")


def text_of(node):
    """Flatten a PARA element to plain text, dropping publisher annotations."""
    parts = []

    def walk(el):
        if el.tag == "HIGHLIGHTER":
            return
        if el.tag == "LINEBRK":
            parts.append("\n")
        if el.text:
            parts.append(el.text)
        for child in el:
            walk(child)
            if child.tail:
                parts.append(child.tail)

    walk(node)
    s = html.unescape("".join(parts))
    s = ALP_NOISE.sub("", s)
    s = s.replace("\u00a0", " ")
    return WS.sub(" ", s).strip()


def split_designator(text):
    for level, pattern in DESIGNATORS:
        m = pattern.match(text)
        if m:
            return level, m.group(1), text[m.end():].strip()
    return None, None, text


def parse_history(note):
    """Return the legislative-history facts needed to build a bill-section recital.

    Rules 3.1.3-3.1.7: cite the law that last amended the provision; if it was
    only ever added, cite the adding law; a redesignation after the last
    amendment must be cited alongside it.
    """
    added = None
    amended = None
    redesignated = None
    repealed = False

    for chunk in note.strip("()").split(";"):
        chunk = chunk.strip()
        m = HISTORY_EVENT.search(chunk) or STATE_EVENT.search(chunk)
        if not m:
            continue
        groups = m.groupdict()
        law = {"number": int(groups["num"]), "year": int(groups["year"])}
        law["state"] = "verb" not in groups
        verb = groups.get("verb") or ""
        if verb.startswith("Am"):
            amended = law
            redesignated = None
        elif verb.startswith("Repealed"):
            repealed = True
        elif verb.startswith("Ren"):
            redesignated = law
        else:
            added = law

    return {
        "added": added,
        "amended": amended,
        "redesignated": redesignated,
        "repealed": repealed,
        "note": note,
    }


def parse_section(record_level, body_level):
    heading_el = record_level.find("RECORD/HEADING")
    if heading_el is None:
        return None
    heading = WS.sub(" ", html.unescape("".join(heading_el.itertext()))).strip()
    m = SECTION_HEADING.match(heading)
    if not m:
        return None

    cite = m.group("cite").rstrip(".")
    section_heading = m.group("heading").strip()

    paragraphs = []
    for record in body_level.findall("RECORD"):
        for para in record.findall("PARA"):
            t = text_of(para)
            if t and not EDITORIAL.match(t):
                paragraphs.append(t)

    history = {"added": None, "amended": None, "redesignated": None,
               "repealed": False, "note": ""}
    if paragraphs and HISTORY_NOTE.match(paragraphs[-1]):
        history = parse_history(paragraphs.pop())

    blocks = []
    for i, text in enumerate(paragraphs):
        level, designator, rest = split_designator(text)
        if i == 0 and level is None:
            # Undivided section: the heading runs into the first sentence.
            blocks.append({"level": "section", "designator": "", "text": rest})
            continue
        blocks.append({
            "level": level or "paragraph",
            "designator": designator or "",
            "text": rest,
        })

    return {
        "cite": cite,
        "heading": section_heading,
        "blocks": blocks,
        "history": history,
    }


def find_sections(xml_dir, wanted):
    wanted = set(wanted)
    found = {}
    for path in sorted(glob.glob(os.path.join(xml_dir, "*.xml"))):
        if not wanted - set(found):
            break
        # cheap prefilter so we do not parse 280MB of XML
        with open(path, "rb") as f:
            blob = f.read()
        if not any(("§ %s " % c).encode() in blob for c in wanted - set(found)):
            continue
        try:
            root = ET.fromstring(blob)
        except ET.ParseError as e:
            print("skip %s: %s" % (path, e), file=sys.stderr)
            continue
        for level in root.iter("LEVEL"):
            if level.get("style-name") != "Section":
                continue
            body = level.find("LEVEL")
            if body is None:
                continue
            parsed = parse_section(level, body)
            if parsed and parsed["cite"] in wanted and parsed["cite"] not in found:
                parsed["source_file"] = os.path.basename(path)
                found[parsed["cite"]] = parsed
    return found


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--xml-dir", required=True)
    p.add_argument("--section", action="append", required=True,
                   help="section number, e.g. 16-497 (repeatable)")
    p.add_argument("--code", default="administrative code",
                   choices=["administrative code", "charter"])
    p.add_argument("--out", default="static/editor/law_fixture.json")
    args = p.parse_args()

    found = find_sections(args.xml_dir, args.section)
    missing = [c for c in args.section if c not in found]
    if missing:
        print("not found: %s" % ", ".join(missing), file=sys.stderr)

    sections = []
    for cite in args.section:
        if cite not in found:
            continue
        s = found[cite]
        s["code"] = args.code
        s["id"] = "%s/%s" % (args.code.replace(" ", "-"), cite)
        sections.append(s)

    out = {
        "generated": date.today().isoformat(),
        "source": "American Legal Publishing XML export of the New York City "
                  "Administrative Code",
        "sections": sections,
    }
    os.makedirs(os.path.dirname(args.out), exist_ok=True)
    with open(args.out, "w") as f:
        json.dump(out, f, indent=2, ensure_ascii=False)
        f.write("\n")
    print("wrote %d sections to %s" % (len(sections), args.out))


if __name__ == "__main__":
    main()

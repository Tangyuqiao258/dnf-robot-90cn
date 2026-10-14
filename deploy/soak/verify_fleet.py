"""Read-only check of what the live robot fleet wears.

Judged against two authorities:
  * the GM tool's own item table (app/cache/item_names_v3.tsv) -- an item the GM
    cannot name is an item whose client window shows blank;
  * the catalog the running robot projected at startup
    (config/pvf/equipment_catalog.json) -- its client_incompatible flag is the
    [add ...]-before-[equipment type] rule that crashes the client.

Flags per worn entry:
  MISSING    id unknown to the GM table or the robot catalog
  EMPTYNAME  record carries no readable [name]
  RARITY5    operator rule: rarity 5 (勇者) is never granted
  FRAGMENT   name carries 后续确认 / 活动 / 租 / 克隆
  INCOMPAT   client_incompatible in the robot catalog

Usage: python -I verify_fleet.py [--db PATH] [--catalog PATH] [--tsv PATH] [--limit N]
"""
import argparse
import json
import os
import sqlite3
import sys
from collections import Counter, defaultdict

DEFAULT_DB = r"G:/90-CN/robot-live-lab/runtime/data/dnf90.db"
DEFAULT_CATALOG = r"G:/90-CN/robot-run/config/pvf/equipment_catalog.json"
DEFAULT_TSV = r"G:/90-CN/DNF90-GM-Portable-win-x64/DNF90-GM-Portable-win-x64/app/cache/item_names_v3.tsv"
FRAGMENTS = ("后续确认", "活动", "租", "克隆")
RARITY5 = "5"


def load_gm_table(path):
    """id -> dict(name, type, rarity, category, level, subtype) from the GM cache."""
    items = {}
    # The GM cache is UTF-8 (its own header pins the PVF sha256 it was built
    # from). Decoding it with any other codepage turns every Chinese name into
    # mojibake, which would silently defeat the name-fragment rules below.
    with open(path, encoding="utf-8-sig", errors="replace") as fh:
        for line in fh:
            parts = line.rstrip("\n").split("\t")
            if len(parts) < 7:
                continue
            try:
                item_id = int(parts[0])
            except ValueError:
                continue
            items[item_id] = {
                "name": parts[1], "type": parts[2], "rarity": parts[3],
                "category": parts[4], "level": parts[5], "subtype": parts[6],
            }
    return items


def load_catalog(path):
    with open(path, encoding="utf-8") as fh:
        return {entry["id"]: entry for entry in json.load(fh)}


def judge(item_id, gm, catalog):
    flags = []
    entry = catalog.get(item_id)
    row = gm.get(item_id)
    if entry is None or row is None:
        flags.append("MISSING")
    name = (entry or {}).get("name") or (row or {}).get("name") or ""
    if not name.strip():
        flags.append("EMPTYNAME")
    if (row or {}).get("rarity") == RARITY5:
        flags.append("RARITY5")
    if any(frag in name for frag in FRAGMENTS):
        flags.append("FRAGMENT")
    if (entry or {}).get("client_incompatible"):
        flags.append("INCOMPAT")
    return flags, name


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--db", default=DEFAULT_DB)
    parser.add_argument("--catalog", default=DEFAULT_CATALOG)
    parser.add_argument("--tsv", default=DEFAULT_TSV)
    parser.add_argument("--limit", type=int, default=12, help="offenders printed per kind")
    parser.add_argument("--show", type=int, action="append", default=[], help="print every slot of this character id")
    args = parser.parse_args()

    for path in (args.db, args.catalog, args.tsv):
        if not os.path.exists(path):
            sys.exit(f"missing input: {path}")

    gm = load_gm_table(args.tsv)
    catalog = load_catalog(args.catalog)
    db = sqlite3.connect(f"file:{args.db}?mode=ro", uri=True)

    characters = db.execute(
        "select character_id, account_id, job, level, grow_type from dnf_characters where delete_flag=0"
    ).fetchall()
    print(f"live characters : {len(characters)}")
    print(f"gm table items  : {len(gm)}")
    print(f"robot catalog   : {len(catalog)}")

    worn = db.execute(
        "select character_id, slot_index, item_id from dnf_equipment_entries"
    ).fetchall()
    pets = db.execute("select character_id, item_id from dnf_pet_entries").fetchall()
    by_char = {row[0]: row for row in characters}

    slot_counts = Counter()
    slot_flags = defaultdict(Counter)
    offenders = defaultdict(list)
    total_flagged = 0
    for character_id, slot, item_id in worn:
        slot_counts[slot] += 1
        flags, name = judge(item_id, gm, catalog)
        if not flags:
            continue
        total_flagged += 1
        for flag in flags:
            slot_flags[slot][flag] += 1
            if len(offenders[flag]) < args.limit:
                char = by_char.get(character_id)
                offenders[flag].append((character_id, char, slot, item_id, name))
    pet_flagged = 0
    for character_id, item_id in pets:
        flags, name = judge(item_id, gm, catalog)
        if not flags:
            continue
        pet_flagged += 1
        for flag in flags:
            if len(offenders["PET:" + flag]) < args.limit:
                offenders["PET:" + flag].append((character_id, by_char.get(character_id), "pet", item_id, name))

    # Self-check: the operator's own offenders must light up. A checker that
    # passes everything must not be trusted to say the fleet is clean.
    print("\nself-check against the items the operator named:")
    for item_id, why in (
        (100343177, "blank 辅助装备 worn by the old fleet"),
        (100352649, "blank 魔法石 worn by the old fleet"),
        (63253, "blank creature worn by the old fleet"),
        (100352094, "blank 魔法石, robot17001001"),
        (100330375, "chn_rental 租 gear, robot17001002"),
        (100330686, "chn_level_event 活动 gear, robot17001002"),
        (100080387, "勇者 rarity 5"),
        (101030240, "ordinary named gear (must stay clean)"),
    ):
        flags, name = judge(item_id, gm, catalog)
        verdict = ",".join(flags) if flags else "clean"
        print(f"  {item_id:>10}  {verdict:<28} {name!r}   # {why}")

    for wanted in args.show:
        char = by_char.get(str(wanted))
        print(f"\ncharacter {wanted}: {char}")
        for character_id, slot, item_id in sorted(worn):
            if character_id != str(wanted):
                continue
            flags, name = judge(item_id, gm, catalog)
            verdict = ",".join(flags) if flags else "ok"
            entry = catalog.get(item_id) or {}
            print(f"   slot {slot:>2} item {item_id:>10} [{verdict}] {name!r} type={entry.get('item_type')} lv={entry.get('level')}")

    print("\nslots in use (slot: entries):")
    for slot in sorted(slot_counts):
        marks = " ".join(f"{flag}={count}" for flag, count in sorted(slot_flags[slot].items()))
        print(f"  slot {slot:>2}: {slot_counts[slot]:>4}   {marks}")

    print(f"\nflagged equipment entries: {total_flagged} / {len(worn)}")
    print(f"flagged pet entries      : {pet_flagged} / {len(pets)}")
    for key in sorted(offenders):
        print(f"\n-- {key} (showing {len(offenders[key])}) --")
        for character_id, char, slot, item_id, name in offenders[key]:
            job = char[2] if char else "?"
            level = char[3] if char else "?"
            print(f"   cid={character_id} job={job} lv={level} slot={slot} item={item_id} name={name!r}")


if __name__ == "__main__":
    main()

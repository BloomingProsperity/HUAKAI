"""Unit tests for h2_capture_server helpers.

Covers:
- ``parse_settings_frame_payload`` (SETTINGS frame body decoder)
- ``annotate_settings`` (RFC 7540 §6.5.2 id → name annotator)
- ``extract_frames`` (TCP-segmentation-safe H2 frame walker)

CLAUDE.md #14 mutation discipline: each test guards a specific defect class
and is designed to FAIL if that defect is introduced.

Run:
    python tools/fingerprint-collector/tests/test_settings_parser.py
"""

from __future__ import annotations

import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

from h2_capture_server import (  # noqa: E402
    annotate_settings,
    extract_frames,
    parse_settings_frame_payload,
)


def test_known_settings_frame() -> None:
    """Baseline: a plausible undici-style SETTINGS payload parses correctly.

    Defect this guards: parser misreads value byte order or shifts by 1 byte.
    """
    payload = bytes.fromhex(
        "0001" + "00001000"   # HEADER_TABLE_SIZE = 4096
        + "0002" + "00000000" # ENABLE_PUSH = 0
        + "0004" + "0000ffff" # INITIAL_WINDOW_SIZE = 65535
    )
    result = parse_settings_frame_payload(payload)
    assert result == [
        {"id": 1, "value": 4096},
        {"id": 2, "value": 0},
        {"id": 4, "value": 65535},
    ], f"unexpected parse result: {result}"


def test_order_preserved_against_canonical_sort() -> None:
    """Send params in NON-canonical order; parser must NOT sort by ID.

    Defect this guards: parser silently re-orders by ascending ID. That
    would destroy the on-wire fingerprint axis F-1.a requires.

    Mutation: if `parse_settings_frame_payload` is replaced with one that
    sorts by ID before returning, this test fails on the first assertion.
    """
    payload = bytes.fromhex(
        "0004" + "0000ffff"  # INITIAL_WINDOW_SIZE FIRST
        + "0001" + "00001000" # HEADER_TABLE_SIZE SECOND
    )
    result = parse_settings_frame_payload(payload)
    assert result[0]["id"] == 4, (
        f"first param must remain id=4 (INITIAL_WINDOW_SIZE), got id={result[0]['id']}"
    )
    assert result[1]["id"] == 1, (
        f"second param must remain id=1 (HEADER_TABLE_SIZE), got id={result[1]['id']}"
    )


def test_mutation_value_change_detected() -> None:
    """Flipping a single value byte must produce a different parse output.

    Defect this guards: parser ignores the value bytes (returns a constant).
    Mutation: if parser hard-codes value=0, base == mutated and this fails.
    """
    base = bytes.fromhex("0001" + "00001000")     # HEADER_TABLE_SIZE = 4096
    mutated = bytes.fromhex("0001" + "00001001")  # HEADER_TABLE_SIZE = 4097
    base_parsed = parse_settings_frame_payload(base)
    mutated_parsed = parse_settings_frame_payload(mutated)
    assert base_parsed != mutated_parsed, (
        "parser produced identical output for inputs that differ by 1 byte; "
        "values are not being read"
    )
    assert mutated_parsed[0]["value"] == 4097, (
        f"mutated value should be 4097, got {mutated_parsed[0]['value']}"
    )


def test_mutation_id_change_detected() -> None:
    """Flipping the identifier byte must produce a different parse output.

    Defect this guards: parser ignores ID bytes (always treats as id=1).
    """
    base = bytes.fromhex("0001" + "00001000")     # id=1
    mutated = bytes.fromhex("0003" + "00001000")  # id=3
    base_parsed = parse_settings_frame_payload(base)
    mutated_parsed = parse_settings_frame_payload(mutated)
    assert base_parsed != mutated_parsed, (
        "parser produced identical output for inputs differing in id byte; "
        "id is not being read"
    )
    assert mutated_parsed[0]["id"] == 3


def test_invalid_length_raises() -> None:
    """SETTINGS payload length not divisible by 6 must raise ValueError."""
    raised = False
    try:
        parse_settings_frame_payload(b"\x00\x01\x00\x00")  # 4 bytes
    except ValueError:
        raised = True
    assert raised, "expected ValueError on length not divisible by 6"


def test_empty_payload_returns_empty() -> None:
    """Empty SETTINGS payload (length 0) is a valid no-op frame."""
    assert parse_settings_frame_payload(b"") == []


def test_annotate_known_id() -> None:
    """annotate_settings names known IDs from RFC 7540 §6.5.2."""
    annotated = annotate_settings([{"id": 1, "value": 4096}, {"id": 2, "value": 0}])
    assert annotated[0]["name"] == "SETTINGS_HEADER_TABLE_SIZE"
    assert annotated[1]["name"] == "SETTINGS_ENABLE_PUSH"


def test_annotate_unknown_id() -> None:
    """annotate_settings preserves unknown IDs with UNKNOWN_0x.... label.

    Defect this guards: annotator silently drops unknown params (would mask
    upstream introducing new SETTINGS we don't know about).
    """
    annotated = annotate_settings([{"id": 0x1234, "value": 42}])
    assert len(annotated) == 1, "unknown-id param must NOT be dropped"
    assert annotated[0]["name"] == "UNKNOWN_0x1234"
    assert annotated[0]["value"] == 42


# Fixture: a 21-byte SETTINGS frame (9-byte header + 12-byte payload of 2 params).
SETTINGS_FRAME_FULL = (
    bytes.fromhex("00000c040000000000")        # header: length=12, type=SETTINGS
    + bytes.fromhex("000200000000000400040000") # payload: ENABLE_PUSH=0 + IWS=262144
)
assert len(SETTINGS_FRAME_FULL) == 21, "fixture must be 21 bytes for these tests"


def test_extract_frames_single_complete() -> None:
    """One complete frame in, one frame out, no leftover."""
    frames, leftover = extract_frames(SETTINGS_FRAME_FULL)
    assert len(frames) == 1
    hdr, payload = frames[0]
    assert hdr == SETTINGS_FRAME_FULL[:9]
    assert payload == SETTINGS_FRAME_FULL[9:]
    assert leftover == b""


def test_extract_frames_multiple_back_to_back() -> None:
    """Two complete frames concatenated parse as two distinct frames."""
    buf = SETTINGS_FRAME_FULL + SETTINGS_FRAME_FULL
    frames, leftover = extract_frames(buf)
    assert len(frames) == 2
    assert leftover == b""
    assert frames[0] == frames[1], "identical fixture should produce identical tuples"


def test_extract_frames_partial_payload_returns_leftover() -> None:
    """Frame whose payload is cut short: 0 frames returned, ALL bytes preserved.

    Defect this guards: the original walker discarded partial-frame bytes and
    started parsing the next recv() chunk at offset 0, mis-parsing the frame
    that was supposed to complete. After this fix, the truncated bytes must
    appear verbatim in `leftover` so the next call (with more data appended)
    can complete the frame.

    Mutation: if `extract_frames` returns `leftover=b""` here (the original
    bug), this test fails on the second assertion.
    """
    truncated = SETTINGS_FRAME_FULL[:15]  # 9-byte header + 6 of 12 payload bytes
    frames, leftover = extract_frames(truncated)
    assert frames == [], "no complete frame; payload is short by 6 bytes"
    assert leftover == truncated, (
        f"all {len(truncated)} bytes must survive in leftover, got {len(leftover)}"
    )


def test_extract_frames_short_header_returns_leftover() -> None:
    """Bytes shorter than a 9-byte frame header: 0 frames, all bytes preserved.

    Mutation: if walker tries to parse a 4-byte buffer as if it had a header,
    it would either crash or invent a bogus length and skip past the buffer.
    """
    short = SETTINGS_FRAME_FULL[:4]  # not even a complete header
    frames, leftover = extract_frames(short)
    assert frames == []
    assert leftover == short


def test_extract_frames_split_reassembles_across_two_calls() -> None:
    """The actual regression scenario: byte split mid-payload across two recv()s.

    This is what the original bug allowed to fail. Now we simulate it directly:
    feed first chunk, get leftover; concat leftover + next chunk, get the frame.

    Mutation: revert the server's `pending` buffer addition and replace with
    `data = ssock.recv()` + immediate `extract_frames(data)`; the second call
    would NOT have the first chunk's bytes, so the frame would be lost.
    """
    chunk1 = SETTINGS_FRAME_FULL[:15]
    chunk2 = SETTINGS_FRAME_FULL[15:]

    frames1, leftover1 = extract_frames(chunk1)
    assert frames1 == []
    assert leftover1 == chunk1

    frames2, leftover2 = extract_frames(leftover1 + chunk2)
    assert len(frames2) == 1, "frame must reassemble exactly once leftover+chunk2 supplies it"
    assert frames2[0][0] + frames2[0][1] == SETTINGS_FRAME_FULL
    assert leftover2 == b""


def test_extract_frames_trailing_partial_after_complete() -> None:
    """One complete frame followed by start-of-next: 1 frame out, partial in leftover.

    This is the common steady-state for a chatty H2 connection: each recv()
    contains some complete frames and possibly a trailing partial.
    """
    buf = SETTINGS_FRAME_FULL + SETTINGS_FRAME_FULL[:7]
    frames, leftover = extract_frames(buf)
    assert len(frames) == 1
    assert leftover == SETTINGS_FRAME_FULL[:7]
    assert len(leftover) == 7


if __name__ == "__main__":
    tests = [
        test_known_settings_frame,
        test_order_preserved_against_canonical_sort,
        test_mutation_value_change_detected,
        test_mutation_id_change_detected,
        test_invalid_length_raises,
        test_empty_payload_returns_empty,
        test_annotate_known_id,
        test_annotate_unknown_id,
        test_extract_frames_single_complete,
        test_extract_frames_multiple_back_to_back,
        test_extract_frames_partial_payload_returns_leftover,
        test_extract_frames_short_header_returns_leftover,
        test_extract_frames_split_reassembles_across_two_calls,
        test_extract_frames_trailing_partial_after_complete,
    ]
    failed = 0
    for t in tests:
        try:
            t()
            print(f"PASS  {t.__name__}")
        except AssertionError as exc:
            print(f"FAIL  {t.__name__}: {exc}")
            failed += 1
    if failed:
        print(f"\n{failed}/{len(tests)} FAILED")
        sys.exit(1)
    print(f"\n{len(tests)}/{len(tests)} PASS")

"""Parsing helpers for simple window specifications."""

import re

_ASCII_NONNEGATIVE = re.compile(r"\A[0-9]+\Z")


def _parse_component(part, original):
    if not _ASCII_NONNEGATIVE.match(part):
        raise ValueError(
            "invalid window component %r in %r" % (part, original)
        )
    return int(part)


def parse_window(text):
    """Parse a window specification into an inclusive ``(start, stop)`` tuple.

    ``"n"`` yields ``(n, n)`` and ``"a:b"`` yields ``(a, b)`` when ``a <= b``.
    Outer whitespace is ignored.  Anything else -- including negative numbers,
    more than one colon, reversed bounds, non-ASCII/non-numeric text, empty
    strings and non-string values -- raises ``ValueError``.
    """
    if isinstance(text, bool) or not isinstance(text, str):
        raise ValueError(
            "parse_window expects a string, got %s" % type(text).__name__
        )

    value = text.strip()
    if not value:
        raise ValueError("empty window specification")

    parts = value.split(":")
    if len(parts) == 1:
        n = _parse_component(parts[0], text)
        return (n, n)
    if len(parts) == 2:
        start = _parse_component(parts[0], text)
        stop = _parse_component(parts[1], text)
        if start > stop:
            raise ValueError("window start %d exceeds stop %d" % (start, stop))
        return (start, stop)
    raise ValueError("window specification has too many ':' separators: %r" % (text,))

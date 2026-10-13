import re

_ASCII_UINT = re.compile(r"[0-9]+")


def parse_window(text):
    if not isinstance(text, str):
        raise ValueError("window must be a string")
    value = text.strip()
    if not value:
        raise ValueError("empty window")
    if ":" in value:
        parts = value.split(":")
        if len(parts) != 2:
            raise ValueError("window must contain a single ':'")
        start_text, end_text = parts
        if not _ASCII_UINT.fullmatch(start_text) or not _ASCII_UINT.fullmatch(end_text):
            raise ValueError("window bounds must be nonnegative integers")
        start, end = int(start_text), int(end_text)
        if start > end:
            raise ValueError("window bounds must be ordered")
        return (start, end)
    if not _ASCII_UINT.fullmatch(value):
        raise ValueError("window must be a nonnegative integer")
    n = int(value)
    return (n, n)

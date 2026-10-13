import re

_DIGITS = re.compile(r"[0-9]+\Z")


def parse_window(text):
    if not isinstance(text, str):
        raise ValueError("window must be a string")
    text = text.strip()
    if not text:
        raise ValueError("window must not be empty")
    if ":" in text:
        parts = text.split(":")
        if len(parts) != 2:
            raise ValueError("window must be 'a:b'")
        start_raw, end_raw = parts
        if not _DIGITS.match(start_raw) or not _DIGITS.match(end_raw):
            raise ValueError("window bounds must be ASCII nonnegative integers")
        start, end = int(start_raw), int(end_raw)
        if start > end:
            raise ValueError("window start must not exceed end")
        return (start, end)
    if not _DIGITS.match(text):
        raise ValueError("window must be an ASCII nonnegative integer")
    value = int(text)
    return (value, value)

import re

_ASCII_INT = re.compile(r"[0-9]+\Z")


def parse_window(text):
    if not isinstance(text, str):
        raise ValueError("window must be a string")
    value = text.strip()
    if ":" in value:
        parts = value.split(":")
        if len(parts) != 2:
            raise ValueError("window must be n or a:b")
        start_s, end_s = parts
    else:
        start_s = end_s = value
    if not _ASCII_INT.match(start_s) or not _ASCII_INT.match(end_s):
        raise ValueError("window bounds must be ASCII nonnegative integers")
    start, end = int(start_s), int(end_s)
    if start > end:
        raise ValueError("window start must not exceed end")
    return start, end

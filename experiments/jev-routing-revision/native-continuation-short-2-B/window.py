import re

_ASCII_INT = re.compile(r"[0-9]+")


def parse_window(text):
    if not isinstance(text, str):
        raise ValueError("window must be a string")
    stripped = text.strip()
    parts = stripped.split(":")
    if len(parts) == 1:
        token = parts[0]
        if not _ASCII_INT.fullmatch(token):
            raise ValueError("invalid window: %r" % (text,))
        value = int(token)
        return (value, value)
    if len(parts) == 2:
        start_token, end_token = parts
        if not _ASCII_INT.fullmatch(start_token):
            raise ValueError("invalid window: %r" % (text,))
        if not _ASCII_INT.fullmatch(end_token):
            raise ValueError("invalid window: %r" % (text,))
        start = int(start_token)
        end = int(end_token)
        if start > end:
            raise ValueError("unordered window: %r" % (text,))
        return (start, end)
    raise ValueError("invalid window: %r" % (text,))

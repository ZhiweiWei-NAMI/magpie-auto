import re

_ASCII_DIGITS = re.compile(r"^[0-9]+$")


def parse_window(text):
    if not isinstance(text, str):
        raise ValueError("window must be a string")
    s = text.strip()
    if not s:
        raise ValueError("window must not be empty")
    if ":" in s:
        parts = s.split(":")
        if len(parts) != 2:
            raise ValueError("window must contain at most one ':'")
        start_s, end_s = parts
        if not _ASCII_DIGITS.match(start_s) or not _ASCII_DIGITS.match(end_s):
            raise ValueError("window bounds must be ASCII nonnegative integers")
        start, end = int(start_s), int(end_s)
        if start > end:
            raise ValueError("window start must not exceed end")
        return (start, end)
    if not _ASCII_DIGITS.match(s):
        raise ValueError("window must be an ASCII nonnegative integer")
    n = int(s)
    return (n, n)

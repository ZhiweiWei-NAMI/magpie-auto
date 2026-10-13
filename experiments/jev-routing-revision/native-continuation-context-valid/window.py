import re

_SINGLE = re.compile(r"[0-9]+")
_RANGE = re.compile(r"[0-9]+:[0-9]+")


def parse_window(text):
    """Parse an ASCII nonnegative window.

    Accepts either a single value ``n`` (yielding ``(n, n)``) or an ordered
    range ``a:b`` with ``a <= b``, ignoring surrounding whitespace. Anything
    else raises ``ValueError``.
    """
    if not isinstance(text, str):
        raise ValueError("window must be a string")
    candidate = text.strip()
    if _SINGLE.fullmatch(candidate):
        value = int(candidate)
        return (value, value)
    if _RANGE.fullmatch(candidate):
        start_text, end_text = candidate.split(":")
        start = int(start_text)
        end = int(end_text)
        if start > end:
            raise ValueError("window range must be ordered")
        return (start, end)
    raise ValueError("invalid window: %r" % (text,))

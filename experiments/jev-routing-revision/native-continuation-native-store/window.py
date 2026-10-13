"""Parsing helpers for window expressions such as ``"3"`` or ``"2:5"``."""


def _parse_uint(token):
    """Return ``int(token)`` for an ASCII nonnegative integer token.

    ``str.isdigit()`` alone accepts non-ASCII digits (e.g. Arabic-Indic
    ``"١٢"`` or superscript ``"²"``), and ``int()`` accepts signs and
    surrounding whitespace (``"+1"``, ``"-0"``, ``" 1 "``).  Requiring the
    token to be both ASCII and all digits rejects all of those.
    """
    if not (token.isascii() and token.isdigit()):
        raise ValueError("not an ASCII nonnegative integer: %r" % (token,))
    return int(token)


def parse_window(text):
    """Parse a window expression into an inclusive ``(low, high)`` tuple.

    * ``"n"`` (an ASCII nonnegative integer) -> ``(n, n)``.
    * ``"a:b"`` -> ``(a, b)`` when ``a <= b``.
    * Outer whitespace, and whitespace around each part, is trimmed, so
      ``" 7 "`` -> ``(7, 7)`` and ``" 2 : 5 "`` -> ``(2, 5)``.

    ``ValueError`` is raised for anything else: non-string or boolean
    input, empty/blank input, negative or signed numbers, ``a > b``,
    more than two colon-separated parts, non-numeric tokens, and a
    leading/trailing colon with a missing part.
    """
    if isinstance(text, bool) or not isinstance(text, str):
        raise ValueError("window must be a string, got %s" % type(text).__name__)

    stripped = text.strip()
    if not stripped:
        raise ValueError("window must not be empty")

    parts = stripped.split(":")
    if len(parts) == 1:
        value = _parse_uint(parts[0].strip())
        return (value, value)
    if len(parts) == 2:
        low = _parse_uint(parts[0].strip())
        high = _parse_uint(parts[1].strip())
        if low > high:
            raise ValueError(
                "window lower bound %d exceeds upper bound %d" % (low, high)
            )
        return (low, high)

    raise ValueError("expected 'n' or 'a:b', got %r" % (text,))

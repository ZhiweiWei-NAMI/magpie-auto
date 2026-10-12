def _parse_int(token, value):
    """Convert one side of a range, reporting failures as a malformed range."""
    try:
        return int(token)
    except ValueError:
        raise ValueError('malformed range: %r' % (value,)) from None


def parse_range(value):
    """Parse a range: single non-negative int n -> (n, n); a-b with a <= b -> (a, b).

    Outer whitespace is stripped; malformed or reversed ranges raise ValueError.

    >>> parse_range('2-4')
    (2, 4)
    >>> parse_range(' 7 ')
    (7, 7)
    """
    text = value.strip()
    if '-' not in text:
        n = _parse_int(text, value)
        if n < 0:
            raise ValueError('range value must be non-negative: %r' % (value,))
        return (n, n)
    parts = text.split('-')
    if len(parts) != 2 or parts[1] == '':
        raise ValueError('malformed range: %r' % (value,))
    if parts[0] == '':
        # A leading '-' followed by a value is a negative number, not a
        # malformed range; report it against the documented contract.
        raise ValueError('range value must be non-negative: %r' % (value,))
    a, b = _parse_int(parts[0], value), _parse_int(parts[1], value)
    if a < 0 or b < 0:
        raise ValueError('range values must be non-negative: %r' % (value,))
    if a > b:
        raise ValueError('reversed range: %r' % (value,))
    return (a, b)

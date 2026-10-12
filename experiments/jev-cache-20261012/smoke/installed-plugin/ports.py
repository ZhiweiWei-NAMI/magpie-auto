def parse_port(value):
    """Parse a TCP/UDP port number.

    Accepts an ``int`` (``bool`` excluded) or a whitespace-trimmed ``str``
    made up solely of ASCII decimal digits.  The resulting number must be in
    the range 1..65535 inclusive.  Any other type or out-of-range value
    raises ``ValueError``.
    """
    if isinstance(value, bool):
        raise ValueError("port must be an int or a decimal string, not bool")
    if isinstance(value, int):
        port = value
    elif isinstance(value, str):
        candidate = value.strip()
        if not candidate or not candidate.isascii() or not candidate.isdigit():
            raise ValueError("port string must contain only ASCII decimal digits")
        port = int(candidate)
    else:
        raise ValueError(
            "port must be an int or a string, got %r" % type(value).__name__
        )
    if not 1 <= port <= 65535:
        raise ValueError("port must be in the range 1..65535, got %d" % port)
    return port

import re

_SINGLE = re.compile(r'^[0-9]+$')
_PAIR = re.compile(r'^([0-9]+):([0-9]+)$')


def parse_window(text):
    if not isinstance(text, str):
        raise ValueError('window must be a string')
    stripped = text.strip()
    match = _PAIR.match(stripped)
    if match:
        start, end = int(match.group(1)), int(match.group(2))
        if start > end:
            raise ValueError('window start must not exceed end')
        return (start, end)
    if _SINGLE.match(stripped):
        value = int(stripped)
        return (value, value)
    raise ValueError('invalid window: %r' % (text,))

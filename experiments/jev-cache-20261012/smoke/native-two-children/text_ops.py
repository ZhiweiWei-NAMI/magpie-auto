def normalize_tag(value):
    """Strip outer whitespace, lowercase, and collapse whitespace runs to '-'.

    >>> normalize_tag('  New York  ')
    'new-york'
    >>> normalize_tag('')
    ''
    """
    return '-'.join(value.strip().lower().split())

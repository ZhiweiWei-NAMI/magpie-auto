def normalize_label(text):
    """Normalize a label: trim outer whitespace, lowercase, join whitespace runs with '-'.

    Examples:
        ' A  B ' -> 'a-b'
        ''       -> ''
    """
    return "-".join(text.split()).lower()

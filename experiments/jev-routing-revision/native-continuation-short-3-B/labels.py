def normalize_label(text):
    """Trim, lowercase, and join whitespace runs with a hyphen."""
    if not isinstance(text, str):
        raise ValueError("label must be a string")
    return "-".join(text.split()).lower()

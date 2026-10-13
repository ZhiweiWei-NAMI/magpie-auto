def normalize_label(text):
    """Trim, lowercase, and join whitespace runs with a hyphen."""
    return "-".join(text.split()).lower()

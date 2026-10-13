def normalize_label(text):
    """Normalize a label: trim, lowercase, and hyphen-join whitespace runs."""
    if not isinstance(text, str):
        raise ValueError("label must be a string")
    return "-".join(text.lower().split())

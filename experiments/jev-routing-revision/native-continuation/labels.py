def normalize_label(text):
    """Trim outer whitespace, lowercase, and join whitespace runs with "-"."""
    return "-".join(text.strip().lower().split())

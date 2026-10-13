def normalize_label(text):
    """Normalize a label: trim, lowercase, and hyphenate whitespace runs."""
    return "-".join(text.split()).lower()

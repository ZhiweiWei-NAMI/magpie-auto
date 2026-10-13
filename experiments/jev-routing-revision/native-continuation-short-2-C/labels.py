def normalize_label(text):
    if not isinstance(text, str):
        raise ValueError("label must be a string")
    return "-".join(text.split()).lower()

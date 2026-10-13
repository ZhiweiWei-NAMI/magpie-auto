import unittest

from window import parse_window


class ParseWindowTests(unittest.TestCase):
    def test_single_value(self):
        self.assertEqual(parse_window("5"), (5, 5))
        self.assertEqual(parse_window("0"), (0, 0))

    def test_range_value(self):
        self.assertEqual(parse_window("2:5"), (2, 5))
        self.assertEqual(parse_window("3:3"), (3, 3))

    def test_outer_whitespace_is_trimmed(self):
        self.assertEqual(parse_window(" 7 "), (7, 7))
        self.assertEqual(parse_window("\t2:5\n"), (2, 5))

    def test_returns_plain_ints(self):
        start, stop = parse_window("2:5")
        self.assertIs(type(start), int)
        self.assertIs(type(stop), int)

    def test_invalid_strings(self):
        for value in ["5:2", "-1", "1:2:3", "x", "", "   ", ":", "1:", ":2", "1.5", "+5", "1 : 2"]:
            with self.assertRaises(ValueError, msg=repr(value)):
                parse_window(value)

    def test_non_ascii_digits_rejected(self):
        with self.assertRaises(ValueError):
            parse_window("\u0665")  # ARABIC-INDIC DIGIT FIVE

    def test_boolean_rejected(self):
        for value in [True, False]:
            with self.assertRaises(ValueError, msg=repr(value)):
                parse_window(value)

    def test_non_string_rejected(self):
        for value in [5, 2.5, None, ("2", "5"), ["2", "5"], object()]:
            with self.assertRaises(ValueError, msg=repr(value)):
                parse_window(value)


if __name__ == "__main__":
    unittest.main()

import unittest
from ports import parse_port
class PortTests(unittest.TestCase):
    def test_bounds(self):
        self.assertEqual(parse_port(1), 1)
        self.assertEqual(parse_port(" 65535 "), 65535)
    def test_invalid_bounds(self):
        for value in (0, -1, 65536):
            with self.assertRaises(ValueError): parse_port(value)
    def test_invalid_types(self):
        for value in (True, False, None, 1.5, [], {}):
            with self.assertRaises(ValueError): parse_port(value)
    def test_invalid_strings(self):
        for value in ("", " ", "1.5", "port", "+2", "-2"):
            with self.assertRaises(ValueError): parse_port(value)
if __name__ == "__main__": unittest.main()

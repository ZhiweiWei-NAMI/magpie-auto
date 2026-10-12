import unittest
from text_ops import normalize_tag
from ranges import parse_range
class Tests(unittest.TestCase):
    def test_tag(self):
        self.assertEqual(normalize_tag('  New York  '), 'new-york')
        self.assertEqual(normalize_tag(''), '')
        self.assertEqual(normalize_tag('A  B'), 'a-b')
    def test_ranges(self):
        self.assertEqual(parse_range('2-4'), (2,4))
        self.assertEqual(parse_range(' 7 '), (7,7))
        for value in ['4-2','1-2-3','x','-1']:
            with self.assertRaises(ValueError):parse_range(value)

    def test_negative_single_uses_documented_message(self):
        with self.assertRaises(ValueError) as cm:
            parse_range('-1')
        self.assertIn('non-negative', str(cm.exception))

    def test_malformed_uses_documented_message(self):
        for value in ['x', 'a-b', '5-', '', '   ']:
            with self.assertRaises(ValueError) as cm:
                parse_range(value)
            self.assertIn('malformed', str(cm.exception))

    def test_negative_bounds_and_reversed_messages(self):
        with self.assertRaises(ValueError) as cm:
            parse_range('1--2')
        self.assertIn('malformed', str(cm.exception))
        with self.assertRaises(ValueError) as cm:
            parse_range('4-2')
        self.assertIn('reversed', str(cm.exception))

    def test_permissive_whitespace_around_separator(self):
        self.assertEqual(parse_range('2 - 4'), (2, 4))

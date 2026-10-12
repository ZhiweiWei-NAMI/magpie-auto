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

import unittest
from window import parse_window
from labels import normalize_label
class Checks(unittest.TestCase):
 def test_window(self):
  self.assertEqual(parse_window('2:5'),(2,5));self.assertEqual(parse_window(' 7 '),(7,7))
 def test_invalid(self):
  for value in ['5:2','-1','1:2:3','x']:
   with self.assertRaises(ValueError):parse_window(value)
 def test_label(self):
  self.assertEqual(normalize_label(' A  B '),'a-b');self.assertEqual(normalize_label(''),'')
class InputValidation(unittest.TestCase):
 def test_booleans_rejected(self):
  self.assertRaises(ValueError, parse_window, True)
  self.assertRaises(ValueError, parse_window, False)
 def test_non_string_types_rejected(self):
  for value in [None, 5, 2.5, 0.5, ['2:5'], ('2', '5')]:
   with self.assertRaises(ValueError):
    parse_window(value)
 def test_more_invalid_strings_rejected(self):
  for value in ['5:2', '-1', '1:2:3', 'x', '', '  ', '+1', '1.5', '2:5:6']:
   with self.assertRaises(ValueError):
    parse_window(value)

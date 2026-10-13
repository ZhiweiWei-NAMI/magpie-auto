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

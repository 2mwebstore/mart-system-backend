-- The company logo is stored as a setting (an image data URL); TEXT tops out
-- at 64 KB, too small for some logos.
ALTER TABLE settings MODIFY value MEDIUMTEXT NOT NULL;

-- settings.rate is the exchange rate's alone again: the till cash currency it
-- also covered for a while (the withdrawn 000036) became the main currency,
-- which only the super admin sets. A database that never ran the old 000036
-- already has this wording.
UPDATE permissions SET description = 'Change the exchange rate' WHERE `key` = 'settings.rate';

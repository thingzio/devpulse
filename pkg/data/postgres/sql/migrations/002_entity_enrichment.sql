-- Reset developer entity from empty string to NULL so the enrichment
-- phase will fetch full GitHub profiles (company field) for all existing
-- developers. After enrichment: NULL = never tried, '' = tried/no company,
-- 'Acme' = has a company on their GitHub profile.
UPDATE developer SET entity = NULL WHERE entity = '' OR entity IS NULL;

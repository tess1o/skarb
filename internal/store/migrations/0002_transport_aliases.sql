-- Add common transport-related aliases to existing installations.
-- Fresh installations get these from Seed after 0001; this migration updates
-- databases that were created before the aliases were added.
INSERT OR IGNORE INTO category_aliases(alias_norm, alias_text, category_id)
SELECT a.alias, a.alias, c.id
FROM (
    SELECT 'шиномонтаж' AS alias
    UNION ALL SELECT 'шини'
    UNION ALL SELECT 'колеса'
    UNION ALL SELECT 'автосервіс'
    UNION ALL SELECT 'ремонт авто'
    UNION ALL SELECT 'мийка'
    UNION ALL SELECT 'парковка'
    UNION ALL SELECT 'заправка'
    UNION ALL SELECT 'проїзд'
    UNION ALL SELECT 'квиток'
    UNION ALL SELECT 'потяг'
    UNION ALL SELECT 'автобус'
    UNION ALL SELECT 'tire service'
    UNION ALL SELECT 'tire'
    UNION ALL SELECT 'tyre'
    UNION ALL SELECT 'car service'
    UNION ALL SELECT 'car wash'
    UNION ALL SELECT 'gas station'
    UNION ALL SELECT 'bus'
    UNION ALL SELECT 'train'
) AS a
JOIN categories c ON c.name = 'Транспорт' AND c.kind = 'expense';

-- A wiki page's parent: the position, in its snapshot's wiki, of the section
-- page it sits under; NULL for a page at the top (the overview, a section, or
-- any page of a wiki planned flat or before this column). The nav shows a
-- section's pages indented under it.
ALTER TABLE wiki_pages ADD COLUMN parent INTEGER;
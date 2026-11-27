#ifndef MELOTROVE_THUMBNAIL_VIPS_H
#define MELOTROVE_THUMBNAIL_VIPS_H

#include <stddef.h>

int artwork_vips_thumbnail(const char *input_path, const char *output_path, int max_dimension, char *error, size_t error_size);

#endif

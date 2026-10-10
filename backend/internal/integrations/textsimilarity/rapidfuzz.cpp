#include <rapidfuzz/fuzz.hpp>

#include <cstddef>
#include <cstdint>
#include <new>
#include <string>

extern "C" int rapidfuzz_token_set_ratio(const std::uint32_t* left, std::size_t left_len,
                                         const std::uint32_t* right, std::size_t right_len,
                                         double* score) noexcept {
    if (score == nullptr || (left_len != 0 && left == nullptr) || (right_len != 0 && right == nullptr)) {
        return 1;
    }
    if (left_len == 0 && right_len == 0) {
        *score = 0;
        return 0;
    }

    try {
        std::u32string left_string;
        left_string.reserve(left_len);
        for (std::size_t i = 0; i < left_len; ++i) {
            left_string.push_back(static_cast<char32_t>(left[i]));
        }
        std::u32string right_string;
        right_string.reserve(right_len);
        for (std::size_t i = 0; i < right_len; ++i) {
            right_string.push_back(static_cast<char32_t>(right[i]));
        }
        *score = rapidfuzz::fuzz::token_set_ratio(left_string, right_string);
        return 0;
    } catch (const std::bad_alloc&) {
        return 2;
    } catch (...) {
        return 3;
    }
}

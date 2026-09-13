# Additional clean files
cmake_minimum_required(VERSION 3.16)

if("${CONFIG}" STREQUAL "" OR "${CONFIG}" STREQUAL "Release")
  file(REMOVE_RECURSE
  "CMakeFiles\\classic_farm_autogen.dir\\AutogenUsed.txt"
  "CMakeFiles\\classic_farm_autogen.dir\\ParseCache.txt"
  "CMakeFiles\\classic_farm_smoke_autogen.dir\\AutogenUsed.txt"
  "CMakeFiles\\classic_farm_smoke_autogen.dir\\ParseCache.txt"
  "classic_farm_autogen"
  "classic_farm_smoke_autogen"
  )
endif()

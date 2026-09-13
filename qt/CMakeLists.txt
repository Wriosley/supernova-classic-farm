cmake_minimum_required(VERSION 3.21)
project(ClassicFarmQt LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

find_package(Qt6 REQUIRED COMPONENTS Core Network WebSockets Widgets)
qt_standard_project_setup()

set(FARM_CLIENT_SOURCES
    src/pendingstore.cpp
    src/farmapiclient.cpp
)

qt_add_executable(classic_farm WIN32
    src/main.cpp
    src/loginwindow.cpp
    src/farmwindow.cpp
    src/mailboxdialog.cpp
    ${FARM_CLIENT_SOURCES}
)
target_include_directories(classic_farm PRIVATE src)
target_link_libraries(classic_farm PRIVATE
    Qt6::Core Qt6::Network Qt6::WebSockets Qt6::Widgets)

qt_add_executable(classic_farm_smoke
    src/smoketest.cpp
    ${FARM_CLIENT_SOURCES}
)
target_include_directories(classic_farm_smoke PRIVATE src)
target_link_libraries(classic_farm_smoke PRIVATE
    Qt6::Core Qt6::Network Qt6::WebSockets)

if (MINGW)
    target_compile_options(classic_farm PRIVATE -finput-charset=UTF-8 -fexec-charset=UTF-8)
    target_compile_options(classic_farm_smoke PRIVATE -finput-charset=UTF-8 -fexec-charset=UTF-8)
endif()
if (MSVC)
    target_compile_options(classic_farm PRIVATE /utf-8)
    target_compile_options(classic_farm_smoke PRIVATE /utf-8)
endif()

USE master;
GO

IF DB_ID(N'Northwind') IS NULL
    CREATE DATABASE Northwind;
GO

USE Northwind;
GO

-- -----------------------------------------------
-- Tables
-- -----------------------------------------------

IF OBJECT_ID(N'dbo.Categories', 'U') IS NULL
CREATE TABLE dbo.Categories (
    CategoryID   INT           NOT NULL IDENTITY(1,1),
    CategoryName NVARCHAR(15)  NOT NULL,
    Description  NVARCHAR(MAX) NULL,
    CONSTRAINT PK_Categories PRIMARY KEY (CategoryID)
);
GO

IF OBJECT_ID(N'dbo.Suppliers', 'U') IS NULL
CREATE TABLE dbo.Suppliers (
    SupplierID   INT           NOT NULL IDENTITY(1,1),
    CompanyName  NVARCHAR(40)  NOT NULL,
    ContactName  NVARCHAR(30)  NULL,
    ContactTitle NVARCHAR(30)  NULL,
    Address      NVARCHAR(60)  NULL,
    City         NVARCHAR(15)  NULL,
    Region       NVARCHAR(15)  NULL,
    PostalCode   NVARCHAR(10)  NULL,
    Country      NVARCHAR(15)  NULL,
    Phone        NVARCHAR(24)  NULL,
    Fax          NVARCHAR(24)  NULL,
    CONSTRAINT PK_Suppliers PRIMARY KEY (SupplierID)
);
GO

IF OBJECT_ID(N'dbo.Products', 'U') IS NULL
CREATE TABLE dbo.Products (
    ProductID       INT          NOT NULL IDENTITY(1,1),
    ProductName     NVARCHAR(40) NOT NULL,
    SupplierID      INT          NULL,
    CategoryID      INT          NULL,
    QuantityPerUnit NVARCHAR(20) NULL,
    UnitPrice       MONEY        NULL CONSTRAINT DF_Products_UnitPrice    DEFAULT (0),
    UnitsInStock    SMALLINT     NULL CONSTRAINT DF_Products_UnitsInStock  DEFAULT (0),
    UnitsOnOrder    SMALLINT     NULL CONSTRAINT DF_Products_UnitsOnOrder  DEFAULT (0),
    ReorderLevel    SMALLINT     NULL CONSTRAINT DF_Products_ReorderLevel  DEFAULT (0),
    Discontinued    BIT          NOT NULL CONSTRAINT DF_Products_Discontinued DEFAULT (0),
    CONSTRAINT PK_Products          PRIMARY KEY (ProductID),
    CONSTRAINT FK_Products_Categories FOREIGN KEY (CategoryID) REFERENCES dbo.Categories (CategoryID),
    CONSTRAINT FK_Products_Suppliers  FOREIGN KEY (SupplierID) REFERENCES dbo.Suppliers  (SupplierID)
);
GO

IF OBJECT_ID(N'dbo.Customers', 'U') IS NULL
CREATE TABLE dbo.Customers (
    CustomerID   NCHAR(5)      NOT NULL,
    CompanyName  NVARCHAR(40)  NOT NULL,
    ContactName  NVARCHAR(30)  NULL,
    ContactTitle NVARCHAR(30)  NULL,
    Address      NVARCHAR(60)  NULL,
    City         NVARCHAR(15)  NULL,
    Region       NVARCHAR(15)  NULL,
    PostalCode   NVARCHAR(10)  NULL,
    Country      NVARCHAR(15)  NULL,
    Phone        NVARCHAR(24)  NULL,
    Fax          NVARCHAR(24)  NULL,
    CONSTRAINT PK_Customers PRIMARY KEY (CustomerID)
);
GO

IF OBJECT_ID(N'dbo.Employees', 'U') IS NULL
CREATE TABLE dbo.Employees (
    EmployeeID      INT          NOT NULL IDENTITY(1,1),
    LastName        NVARCHAR(20) NOT NULL,
    FirstName       NVARCHAR(10) NOT NULL,
    Title           NVARCHAR(30) NULL,
    TitleOfCourtesy NVARCHAR(25) NULL,
    BirthDate       DATETIME     NULL,
    HireDate        DATETIME     NULL,
    Address         NVARCHAR(60) NULL,
    City            NVARCHAR(15) NULL,
    Region          NVARCHAR(15) NULL,
    PostalCode      NVARCHAR(10) NULL,
    Country         NVARCHAR(15) NULL,
    HomePhone       NVARCHAR(24) NULL,
    ReportsTo       INT          NULL,
    CONSTRAINT PK_Employees           PRIMARY KEY (EmployeeID),
    CONSTRAINT FK_Employees_ReportsTo FOREIGN KEY (ReportsTo) REFERENCES dbo.Employees (EmployeeID)
);
GO

IF OBJECT_ID(N'dbo.Shippers', 'U') IS NULL
CREATE TABLE dbo.Shippers (
    ShipperID   INT          NOT NULL IDENTITY(1,1),
    CompanyName NVARCHAR(40) NOT NULL,
    Phone       NVARCHAR(24) NULL,
    CONSTRAINT PK_Shippers PRIMARY KEY (ShipperID)
);
GO

IF OBJECT_ID(N'dbo.Orders', 'U') IS NULL
CREATE TABLE dbo.Orders (
    OrderID        INT          NOT NULL IDENTITY(1,1),
    CustomerID     NCHAR(5)     NULL,
    EmployeeID     INT          NULL,
    OrderDate      DATETIME     NULL,
    RequiredDate   DATETIME     NULL,
    ShippedDate    DATETIME     NULL,
    ShipVia        INT          NULL,
    Freight        MONEY        NULL CONSTRAINT DF_Orders_Freight DEFAULT (0),
    ShipName       NVARCHAR(40) NULL,
    ShipAddress    NVARCHAR(60) NULL,
    ShipCity       NVARCHAR(15) NULL,
    ShipRegion     NVARCHAR(15) NULL,
    ShipPostalCode NVARCHAR(10) NULL,
    ShipCountry    NVARCHAR(15) NULL,
    CONSTRAINT PK_Orders           PRIMARY KEY (OrderID),
    CONSTRAINT FK_Orders_Customers FOREIGN KEY (CustomerID) REFERENCES dbo.Customers (CustomerID),
    CONSTRAINT FK_Orders_Employees FOREIGN KEY (EmployeeID) REFERENCES dbo.Employees (EmployeeID),
    CONSTRAINT FK_Orders_Shippers  FOREIGN KEY (ShipVia)    REFERENCES dbo.Shippers  (ShipperID)
);
GO

IF OBJECT_ID(N'dbo.OrderDetails', 'U') IS NULL
CREATE TABLE dbo.OrderDetails (
    OrderID   INT      NOT NULL,
    ProductID INT      NOT NULL,
    UnitPrice MONEY    NOT NULL CONSTRAINT DF_OrderDetails_UnitPrice DEFAULT (0),
    Quantity  SMALLINT NOT NULL CONSTRAINT DF_OrderDetails_Quantity  DEFAULT (1),
    Discount  REAL     NOT NULL CONSTRAINT DF_OrderDetails_Discount  DEFAULT (0),
    CONSTRAINT PK_OrderDetails          PRIMARY KEY (OrderID, ProductID),
    CONSTRAINT FK_OrderDetails_Orders   FOREIGN KEY (OrderID)   REFERENCES dbo.Orders   (OrderID),
    CONSTRAINT FK_OrderDetails_Products FOREIGN KEY (ProductID) REFERENCES dbo.Products (ProductID),
    CONSTRAINT CK_OrderDetails_Discount  CHECK (Discount  >= 0 AND Discount <= 1),
    CONSTRAINT CK_OrderDetails_Quantity  CHECK (Quantity  > 0),
    CONSTRAINT CK_OrderDetails_UnitPrice CHECK (UnitPrice >= 0)
);
GO

IF NOT EXISTS (SELECT 1 FROM dbo.Categories)
BEGIN
    INSERT INTO dbo.Categories (CategoryName, Description) VALUES
        (N'Beverages',      N'Soft drinks, coffees, teas, beers, and ales'),
        (N'Condiments',     N'Sweet and savory sauces, relishes, spreads, and seasonings'),
        (N'Confections',    N'Desserts, candies, and sweet breads'),
        (N'Dairy Products', N'Cheeses'),
        (N'Grains/Cereals', N'Breads, crackers, pasta, and cereal'),
        (N'Meat/Poultry',   N'Prepared meats'),
        (N'Produce',        N'Dried fruit and bean curd'),
        (N'Seafood',        N'Seaweed and fish');
END;
GO

IF NOT EXISTS (SELECT 1 FROM dbo.Suppliers)
BEGIN
    INSERT INTO dbo.Suppliers (CompanyName, ContactName, ContactTitle, Address, City, Country, Phone) VALUES
        (N'Exotic Liquids',             N'Charlotte Cooper',  N'Purchasing Manager',     N'49 Gilbert St.',      N'London',      N'UK',    N'(171) 555-2222'),
        (N'New Orleans Cajun Delights', N'Shelley Burke',     N'Order Administrator',    N'P.O. Box 78934',      N'New Orleans', N'USA',   N'(100) 555-4822'),
        (N'Grandma Kelly''s Homestead', N'Regina Murphy',     N'Sales Representative',   N'707 Oxford Rd.',      N'Ann Arbor',   N'USA',   N'(313) 555-5735'),
        (N'Tokyo Traders',              N'Yoshi Nagase',      N'Marketing Manager',      N'9-8 Sekimai',         N'Tokyo',       N'Japan', N'(03) 3555-5011'),
        (N'Cooperativa de Quesos',      N'Antonio del Valle', N'Export Administrator',   N'Calle del Rosal 4',   N'Oviedo',      N'Spain', N'(98) 598 76 54'),
        (N'Mayumi''s',                  N'Mayumi Ohna',       N'Marketing Representative',N'92 Setsuko Chuo-ku', N'Osaka',       N'Japan', N'(06) 431-7877');
END;
GO

IF NOT EXISTS (SELECT 1 FROM dbo.Products)
BEGIN
    INSERT INTO dbo.Products (ProductName, SupplierID, CategoryID, QuantityPerUnit, UnitPrice, UnitsInStock, UnitsOnOrder, ReorderLevel, Discontinued) VALUES
        (N'Chai',                            1, 1, N'10 boxes x 20 bags',    18.00,  39,  0, 10, 0),
        (N'Chang',                           1, 1, N'24 - 12 oz bottles',    19.00,  17, 40, 25, 0),
        (N'Aniseed Syrup',                   1, 2, N'12 - 550 ml bottles',   10.00,  13, 70, 25, 0),
        (N'Chef Anton''s Cajun Seasoning',   2, 2, N'48 - 6 oz jars',        22.00,  53,  0,  0, 0),
        (N'Grandma''s Boysenberry Spread',   3, 2, N'12 - 8 oz jars',        25.00, 120,  0, 25, 0),
        (N'Uncle Bob''s Organic Dried Pears',3, 7, N'12 - 1 lb pkgs.',       30.00,  15,  0, 10, 0),
        (N'Northwoods Cranberry Sauce',      3, 2, N'12 - 12 oz jars',       40.00,   6,  0,  0, 0),
        (N'Mishi Kobe Niku',                 4, 6, N'18 - 500 g pkgs.',      97.00,  29,  0,  0, 1),
        (N'Tofu',                            4, 7, N'40 - 100 g pkgs.',      23.25,  35,  0,  0, 0),
        (N'Ikura',                           4, 8, N'12 - 200 ml jars',      31.00,  31,  0,  0, 0),
        (N'Queso Cabrales',                  5, 4, N'1 kg pkg.',             21.00,  22, 30, 30, 0),
        (N'Queso Manchego La Pastora',       5, 4, N'10 - 500 g pkgs.',      38.00,  86,  0,  0, 0),
        (N'Konbu',                           6, 8, N'2 kg box',               6.00,  24,  0,  5, 0),
        (N'Genen Shouyu',                    6, 2, N'24 - 250 ml bottles',   15.50,  39,  0,  5, 0),
        (N'Gula Malacca',                    3, 2, N'20 - 2 kg bags',        19.45, 112, 20, 25, 0),
        (N'Pavlova',                         2, 3, N'32 - 500 g boxes',      17.45,  29,  0, 10, 0),
        (N'Alice Mutton',                    4, 6, N'20 - 1 kg tins',        39.00,   0,  0,  0, 1),
        (N'Carnarvon Tigers',                6, 8, N'16 kg pkg.',            62.50,  42,  0,  0, 0),
        (N'Teatime Chocolate Biscuits',      6, 3, N'10 boxes x 12 pieces',   9.20,  25,  0,  5, 0),
        (N'Sir Rodney''s Marmalade',         2, 3, N'30 gift boxes',         81.00,  40,  0,  0, 0);
END;
GO

IF NOT EXISTS (SELECT 1 FROM dbo.Customers)
BEGIN
    INSERT INTO dbo.Customers (CustomerID, CompanyName, ContactName, ContactTitle, Address, City, Region, PostalCode, Country, Phone) VALUES
        (N'ALFKI', N'Alfreds Futterkiste',        N'Maria Anders',       N'Sales Representative', N'Obere Str. 57',                 N'Berlin',       NULL,  N'12209',    N'Germany',     N'030-0074321'),
        (N'ANATR', N'Ana Trujillo Emparedados',   N'Ana Trujillo',       N'Owner',                N'Avda. de la Constitución 2222', N'México D.F.',  NULL,  N'05021',    N'Mexico',      N'(5) 555-4729'),
        (N'ANTON', N'Antonio Moreno Taquería',    N'Antonio Moreno',     N'Owner',                N'Mataderos 2312',                N'México D.F.',  NULL,  N'05023',    N'Mexico',      N'(5) 555-3932'),
        (N'AROUT', N'Around the Horn',            N'Thomas Hardy',       N'Sales Representative', N'120 Hanover Sq.',               N'London',       NULL,  N'WA1 1DP',  N'UK',          N'(171) 555-7788'),
        (N'BERGS', N'Berglunds snabbköp',         N'Christina Berglund', N'Order Administrator',  N'Berguvsvägen 8',                N'Luleå',        NULL,  N'S-958 22', N'Sweden',      N'0921-12 34 65'),
        (N'BLAUS', N'Blauer See Delikatessen',    N'Hanna Moos',         N'Sales Representative', N'Forsterstr. 57',                N'Mannheim',     NULL,  N'68306',    N'Germany',     N'0621-08460'),
        (N'BLONP', N'Blondesddsl père et fils',   N'Frédérique Citeaux', N'Marketing Manager',    N'24, place Kléber',              N'Strasbourg',   NULL,  N'67000',    N'France',      N'88.60.15.31'),
        (N'BOLID', N'Bólido Comidas preparadas',  N'Martín Sommer',      N'Owner',                N'C/ Araquil, 67',                N'Madrid',       NULL,  N'28023',    N'Spain',       N'(91) 555 22 82'),
        (N'BONAP', N'Bon app''',                  N'Laurence Lebihan',   N'Owner',                N'12, rue des Bouchers',          N'Marseille',    NULL,  N'13008',    N'France',      N'91.24.45.40'),
        (N'BOTTM', N'Bottom-Dollar Markets',      N'Elizabeth Lincoln',  N'Accounting Manager',   N'23 Tsawassen Blvd.',            N'Tsawassen',    N'BC', N'T2F 8M4',  N'Canada',      N'(604) 555-4729'),
        (N'BSBEV', N'B''s Beverages',             N'Victoria Ashworth',  N'Sales Representative', N'Fauntleroy Circus',             N'London',       NULL,  N'EC2 5NT',  N'UK',          N'(171) 555-1212'),
        (N'CACTU', N'Cactus Comidas para llevar', N'Patricio Simpson',   N'Sales Agent',          N'Cerrito 333',                   N'Buenos Aires', NULL,  N'1010',     N'Argentina',   N'(1) 135-5555'),
        (N'CENTC', N'Centro comercial Moctezuma', N'Francisco Chang',    N'Marketing Manager',    N'Sierras de Granada 9993',       N'México D.F.',  NULL,  N'05022',    N'Mexico',      N'(5) 555-3392'),
        (N'CHOPS', N'Chop-suey Chinese',          N'Yang Wang',          N'Owner',                N'Hauptstr. 29',                  N'Bern',         NULL,  N'3012',     N'Switzerland', N'0452-076545'),
        (N'COMMI', N'Comércio Mineiro',           N'Pedro Afonso',       N'Sales Associate',      N'Av. dos Lusíadas, 23',          N'São Paulo',    N'SP', N'05432-043',N'Brazil',      N'(11) 555-7647');
END;
GO

IF NOT EXISTS (SELECT 1 FROM dbo.Employees)
BEGIN
    -- Insert all employees without ReportsTo to avoid FK ordering issues.
    INSERT INTO dbo.Employees (LastName, FirstName, Title, TitleOfCourtesy, BirthDate, HireDate, Address, City, Region, PostalCode, Country, HomePhone) VALUES
        (N'Davolio',   N'Nancy',    N'Sales Representative',     N'Ms.',  '1968-12-08', '1992-05-01', N'507 - 20th Ave. E. Apt. 2A', N'Seattle',  N'WA', N'98122',   N'USA', N'(206) 555-9857'),
        (N'Fuller',    N'Andrew',   N'Vice President, Sales',    N'Dr.',  '1952-02-19', '1992-08-14', N'908 W. Capital Way',         N'Tacoma',   N'WA', N'98401',   N'USA', N'(206) 555-9482'),
        (N'Leverling', N'Janet',    N'Sales Representative',     N'Ms.',  '1963-08-30', '1992-04-01', N'722 Moss Bay Blvd.',         N'Kirkland', N'WA', N'98033',   N'USA', N'(206) 555-3412'),
        (N'Peacock',   N'Margaret', N'Sales Representative',     N'Mrs.', '1958-09-19', '1993-05-03', N'4110 Old Redmond Rd.',       N'Redmond',  N'WA', N'98052',   N'USA', N'(206) 555-8122'),
        (N'Buchanan',  N'Steven',   N'Sales Manager',            N'Mr.',  '1955-03-04', '1993-10-17', N'14 Garrett Hill',            N'London',   NULL,  N'SW1 8JR', N'UK',  N'(71) 555-4848'),
        (N'Suyama',    N'Michael',  N'Sales Representative',     N'Mr.',  '1963-07-02', '1993-10-17', N'Coventry House Miner Rd.',   N'London',   NULL,  N'EC2 7JR', N'UK',  N'(71) 555-7773'),
        (N'King',      N'Robert',   N'Sales Representative',     N'Mr.',  '1960-05-29', '1994-01-02', N'Edgeham Hollow',             N'London',   NULL,  N'RG1 9SP', N'UK',  N'(71) 555-5598'),
        (N'Callahan',  N'Laura',    N'Inside Sales Coordinator', N'Ms.',  '1958-01-09', '1994-03-05', N'4726 - 11th Ave. N.E.',      N'Seattle',  N'WA', N'98105',   N'USA', N'(206) 555-1189'),
        (N'Dodsworth', N'Anne',     N'Sales Representative',     N'Ms.',  '1969-01-27', '1994-11-15', N'7 Houndstooth Rd.',          N'London',   NULL,  N'WG2 7LT', N'UK',  N'(71) 555-4444');

    -- Davolio, Leverling, Peacock, Callahan report to Fuller (2).
    UPDATE dbo.Employees SET ReportsTo = 2 WHERE EmployeeID IN (1, 3, 4, 8);
    -- Suyama, King, Dodsworth report to Buchanan (5).
    UPDATE dbo.Employees SET ReportsTo = 5 WHERE EmployeeID IN (6, 7, 9);
END;
GO

IF NOT EXISTS (SELECT 1 FROM dbo.Shippers)
BEGIN
    INSERT INTO dbo.Shippers (CompanyName, Phone) VALUES
        (N'Speedy Express',   N'(503) 555-9831'),
        (N'United Package',   N'(503) 555-3199'),
        (N'Federal Shipping', N'(503) 555-9931');
END;
GO

IF NOT EXISTS (SELECT 1 FROM dbo.Orders)
BEGIN
    INSERT INTO dbo.Orders (CustomerID, EmployeeID, OrderDate, RequiredDate, ShippedDate, ShipVia, Freight, ShipName, ShipAddress, ShipCity, ShipRegion, ShipPostalCode, ShipCountry) VALUES
        (N'ALFKI', 5, '2024-01-04', '2024-02-01', '2024-01-16', 3,  32.38, N'Alfreds Futterkiste',        N'Obere Str. 57',                 N'Berlin',       NULL,  N'12209',    N'Germany'),
        (N'ANATR', 6, '2024-01-05', '2024-02-16', '2024-01-10', 1,  11.61, N'Ana Trujillo Emparedados',   N'Avda. de la Constitución 2222', N'México D.F.', NULL,  N'05021',    N'Mexico'),
        (N'ANTON', 4, '2024-01-08', '2024-02-05', '2024-01-12', 2,  65.83, N'Antonio Moreno Taquería',   N'Mataderos 2312',                N'México D.F.', NULL,  N'05023',    N'Mexico'),
        (N'AROUT', 3, '2024-01-08', '2024-02-05', '2024-01-15', 1,  41.34, N'Around the Horn',           N'120 Hanover Sq.',               N'London',       NULL,  N'WA1 1DP',  N'UK'),
        (N'BERGS', 4, '2024-01-09', '2024-02-06', '2024-01-11', 2,  51.30, N'Berglunds snabbköp',        N'Berguvsvägen 8',                N'Luleå',        NULL,  N'S-958 22', N'Sweden'),
        (N'BLAUS', 3, '2024-01-10', '2024-01-24', '2024-01-16', 2,  58.17, N'Blauer See Delikatessen',   N'Forsterstr. 57',                N'Mannheim',     NULL,  N'68306',    N'Germany'),
        (N'BLONP', 5, '2024-01-11', '2024-02-08', '2024-01-23', 2,  22.98, N'Blondesddsl père et fils',  N'24, place Kléber',              N'Strasbourg',   NULL,  N'67000',    N'France'),
        (N'BOLID', 9, '2024-01-12', '2024-02-09', '2024-01-15', 1, 148.33, N'Bólido Comidas preparadas', N'C/ Araquil, 67',                N'Madrid',       NULL,  N'28023',    N'Spain'),
        (N'BONAP', 3, '2024-01-15', '2024-02-12', '2024-01-17', 2,  13.97, N'Bon app',                   N'12, rue des Bouchers',          N'Marseille',    NULL,  N'13008',    N'France'),
        (N'BOTTM', 4, '2024-01-16', '2024-02-13', '2024-01-22', 3,  81.91, N'Bottom-Dollar Markets',     N'23 Tsawassen Blvd.',            N'Tsawassen',    N'BC', N'T2F 8M4',  N'Canada'),
        (N'BSBEV', 1, '2024-01-17', '2024-02-14', '2024-01-23', 1,  25.50, N'B''s Beverages',            N'Fauntleroy Circus',             N'London',       NULL,  N'EC2 5NT',  N'UK'),
        (N'CACTU', 7, '2024-01-19', '2024-02-16', '2024-01-25', 3,  18.44, N'Cactus Comidas para llevar',N'Cerrito 333',                   N'Buenos Aires', NULL,  N'1010',     N'Argentina'),
        (N'CHOPS', 8, '2024-01-22', '2024-02-19', '2024-01-28', 2,  33.25, N'Chop-suey Chinese',         N'Hauptstr. 29',                  N'Bern',         NULL,  N'3012',     N'Switzerland'),
        (N'COMMI', 2, '2024-01-23', '2024-02-20', '2024-01-29', 1,  12.69, N'Comércio Mineiro',          N'Av. dos Lusíadas, 23',          N'São Paulo',    N'SP', N'05432-043',N'Brazil'),
        (N'ALFKI', 6, '2024-02-01', '2024-03-01', '2024-02-08', 2,  44.10, N'Alfreds Futterkiste',       N'Obere Str. 57',                 N'Berlin',       NULL,  N'12209',    N'Germany');
END;
GO

IF NOT EXISTS (SELECT 1 FROM dbo.OrderDetails)
BEGIN
    INSERT INTO dbo.OrderDetails (OrderID, ProductID, UnitPrice, Quantity, Discount) VALUES
        -- Order 1
        ( 1,  1, 18.00, 12, 0.00),
        ( 1,  2, 19.00,  5, 0.00),
        ( 1,  5, 25.00, 10, 0.10),
        -- Order 2
        ( 2,  3, 10.00, 20, 0.00),
        ( 2,  7, 40.00,  6, 0.25),
        -- Order 3
        ( 3,  9, 23.25, 15, 0.00),
        ( 3, 11, 21.00,  8, 0.00),
        ( 3, 13,  6.00, 30, 0.10),
        -- Order 4
        ( 4,  4, 22.00, 25, 0.00),
        ( 4, 12, 38.00,  5, 0.05),
        -- Order 5
        ( 5,  6, 30.00,  8, 0.00),
        ( 5, 10, 31.00, 12, 0.15),
        -- Order 6
        ( 6, 14, 15.50, 10, 0.00),
        ( 6,  8, 97.00,  2, 0.00),
        -- Order 7
        ( 7, 16, 17.45, 20, 0.00),
        ( 7, 19,  9.20, 15, 0.05),
        -- Order 8
        ( 8, 20, 81.00,  3, 0.00),
        ( 8, 15, 19.45, 10, 0.10),
        -- Order 9
        ( 9,  1, 18.00,  6, 0.00),
        ( 9,  3, 10.00, 18, 0.00),
        -- Order 10
        (10,  2, 19.00, 24, 0.05),
        (10,  6, 30.00,  9, 0.00),
        (10, 18, 62.50,  4, 0.00),
        -- Order 11
        (11,  1, 18.00,  7, 0.00),
        (11,  5, 25.00,  3, 0.00),
        -- Order 12
        (12,  9, 23.25, 11, 0.00),
        (12, 14, 15.50,  8, 0.10),
        -- Order 13
        (13, 11, 21.00,  5, 0.00),
        (13, 16, 17.45, 20, 0.00),
        -- Order 14
        (14,  4, 22.00, 14, 0.00),
        (14, 19,  9.20, 30, 0.05),
        -- Order 15
        (15,  2, 19.00, 10, 0.00),
        (15, 12, 38.00,  6, 0.15);
END;
GO
